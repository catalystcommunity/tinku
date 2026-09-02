"""Runnerlib lifecycle jobs for the tinku repository.

Every CI job in this repository is one branch of this plugin, selected by
the ``TINKU_JOB`` environment variable in the job file. The jobs are Python
here rather than shell in a job file for two reasons: the setup steps
(toolchains, a Postgres cluster) are shared between jobs and would otherwise
be copied between them, and a failure in the middle of a shell block is
easy to lose.
"""

from __future__ import annotations

import base64
import json
import os
import platform
import re
import shutil
import subprocess
import tarfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import List, Mapping, Sequence

from src.logging import log_stdout
from src.plugins import Plugin, PluginContext, PluginPhase


# The Postgres tinku's own tests expect. It matches docker-compose.yaml so a
# developer reading a CI failure sees the URI they already know.
PG_USER = "tinku"
PG_DATABASE = "tinku_db"
PG_PORT = "5432"
PG_TEST_URI = f"postgresql://{PG_USER}@127.0.0.1:{PG_PORT}/{PG_DATABASE}?sslmode=disable"

# Conventional Commits, as the other repositories here check it.
CONVENTIONAL_SUBJECT = re.compile(
    r"^(build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test)"
    r"(\([^()]+\))?!?: .+"
)


def _repo_root(context: PluginContext) -> Path:
    configured = Path(context.config.code_dir)
    if configured.exists():
        return configured.resolve()
    source_path = context.metadata.get("source_path")
    if source_path:
        return Path(source_path).resolve()
    return Path("/job/src")


def _run(
    args: Sequence[str | Path],
    *,
    cwd: Path,
    env: Mapping[str, str] | None = None,
    capture: bool = False,
    check: bool = True,
) -> subprocess.CompletedProcess[str]:
    """Run a command, and make sure its output reaches the job log.

    Output is ALWAYS captured and then re-logged, never left to the
    subprocess's own file descriptors. Runnerlib collects the plugin's log
    calls; anything a child writes straight to the container's stdout can
    arrive out of order or not at all — which turns a failing command into
    "exit status 1" and nothing else.
    """
    command = tuple(str(arg) for arg in args)
    log_stdout(f"+ {' '.join(command)}")
    command_env = os.environ.copy()
    if env:
        command_env.update(env)

    completed = subprocess.run(
        command,
        cwd=cwd,
        env=command_env,
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
    )
    if not capture or completed.returncode != 0:
        for line in (completed.stdout or "").splitlines():
            log_stdout(f"  {line}")
    if check and completed.returncode != 0:
        raise RuntimeError(
            f"{' '.join(command)} failed with exit status {completed.returncode}"
        )
    return completed


def _section(title: str) -> None:
    log_stdout("")
    log_stdout(f"=== {title} ===")


# ---------------------------------------------------------------------------
# Toolchains
# ---------------------------------------------------------------------------


def _ensure_go(root: Path) -> None:
    """Make a Go matching go.mod available.

    The runner image may carry a Go, and it may be older than the toolchain
    line in api/go.mod. Rather than guess, this asks Go itself: GOTOOLCHAIN
    left at its default lets the go command fetch the toolchain a module
    asks for.
    """
    if not shutil.which("go"):
        raise RuntimeError(
            "No Go toolchain on PATH. The runner image is expected to provide one."
        )

    # The image's default GOPATH is /go, which the job's user cannot write.
    # Point the three caches at a writable directory instead. Set here
    # rather than in each job file so a new Go job cannot forget it.
    cache = Path("/job/cache")
    try:
        cache.mkdir(parents=True, exist_ok=True)
    except PermissionError:
        cache = Path(os.environ.get("HOME", "/tmp")) / ".cache" / "tinku-ci"
        cache.mkdir(parents=True, exist_ok=True)
    os.environ["GOPATH"] = str(cache / "go")
    os.environ["GOMODCACHE"] = str(cache / "go" / "pkg" / "mod")
    os.environ["GOCACHE"] = str(cache / "go-build")
    log_stdout(f"Go caches under {cache}")

    result = _run(["go", "version"], cwd=root, capture=True)
    log_stdout(result.stdout.strip())


# The runner image carries Go but no Node, so CI installs it. Pinned to the
# version local development uses: the web client has no `engines` field, so
# the version the tests are known to pass on is the only honest reference.
NODE_VERSION = "26.1.0"


def _ensure_node(root: Path) -> None:
    """Make Node available, installing the pinned release when it is not."""
    if shutil.which("npm"):
        result = _run(["node", "--version"], cwd=root, capture=True)
        log_stdout(f"node {result.stdout.strip()} (from the image)")
        return

    _section(f"Installing Node {NODE_VERSION}")
    home = Path(os.environ.get("HOME", "/root"))
    target = home / ".local" / "node"
    target.mkdir(parents=True, exist_ok=True)

    machine = platform.machine()
    arch = {"x86_64": "x64", "aarch64": "arm64"}.get(machine)
    if arch is None:
        raise RuntimeError(f"No pinned Node build for architecture {machine}")

    name = f"node-v{NODE_VERSION}-linux-{arch}"
    url = f"https://nodejs.org/dist/v{NODE_VERSION}/{name}.tar.xz"
    archive = Path("/tmp") / f"{name}.tar.xz"
    log_stdout(f"downloading {url}")
    urllib.request.urlretrieve(url, archive)

    with tarfile.open(archive) as tar:
        tar.extractall(target)
    archive.unlink()

    node_bin = target / name / "bin"
    os.environ["PATH"] = f"{node_bin}:{os.environ.get('PATH', '')}"

    result = _run(["node", "--version"], cwd=root, capture=True)
    log_stdout(f"node {result.stdout.strip()}")


def _ensure_uv(root: Path) -> Path:
    """Return the uv the site build runs PySocha through.

    website/tools.sh calls `uv tool run`, not the `uvx` alias, so only
    `uv` itself has to exist — see the note in website/tools.sh.
    """
    existing = shutil.which("uv")
    if existing:
        result = _run(["uv", "--version"], cwd=root, capture=True)
        log_stdout(result.stdout.strip())
        return Path(existing)

    _section("Installing uv")
    home = Path(os.environ.get("HOME", "/home/runner"))
    install_dir = home / ".local" / "bin"
    install_dir.mkdir(parents=True, exist_ok=True)
    with urllib.request.urlopen("https://astral.sh/uv/install.sh") as response:
        script = response.read().decode()
    _run(
        ["sh", "-c", script],
        cwd=root,
        env={"UV_INSTALL_DIR": str(install_dir)},
    )
    os.environ["PATH"] = f"{install_dir}:{os.environ.get('PATH', '')}"
    return install_dir / "uv"


def _npm_install(root: Path) -> None:
    """Install the web client's dependencies.

    `npm ci` needs the lockfile to agree with package.json exactly, which is
    what we want in CI: a drifted lockfile should fail here rather than
    resolve to something nobody has run.
    """
    _section("Installing web client dependencies")
    _run(["npm", "ci"], cwd=root / "webapp")


# ---------------------------------------------------------------------------
# Postgres
# ---------------------------------------------------------------------------


def _postgres_bin() -> Path:
    """Find the Postgres binaries the Debian packages hide under a version."""
    for candidate in sorted(Path("/usr/lib/postgresql").glob("*/bin"), reverse=True):
        if (candidate / "initdb").exists():
            return candidate
    raise RuntimeError("No Postgres installation found under /usr/lib/postgresql")


def _start_postgres(root: Path) -> None:
    """Start a throwaway Postgres cluster for the run.

    A cluster in the job rather than a service container: the runner offers
    no services, and the tests need only one database that dies with the
    job. Trust authentication is safe precisely because it never outlives
    the job.
    """
    _section("Installing PostgreSQL")
    _run(["sudo", "apt-get", "update"], cwd=root)
    _run(
        [
            "sudo", "apt-get", "install", "-y", "--no-install-recommends",
            "postgresql", "postgresql-client",
        ],
        cwd=root,
    )

    _section("Starting PostgreSQL")
    pg_bin = _postgres_bin()
    os.environ["PATH"] = f"{pg_bin}:{os.environ.get('PATH', '')}"
    data_dir = Path("/tmp/pgdata")

    _run(
        [pg_bin / "initdb", "-D", data_dir, "--auth=trust", f"--username={PG_USER}"],
        cwd=root,
    )
    _run(
        [
            pg_bin / "pg_ctl", "-D", data_dir, "-l", "/tmp/pg.log",
            "-o", f"-k /tmp -h 127.0.0.1 -p {PG_PORT}", "start",
        ],
        cwd=root,
    )

    # pg_ctl returns before the server accepts connections.
    for _ in range(30):
        ready = _run(
            [pg_bin / "pg_isready", "-h", "127.0.0.1", "-p", PG_PORT, "-U", PG_USER],
            cwd=root,
            capture=True,
            check=False,
        )
        if ready.returncode == 0:
            break
        time.sleep(1)
    else:
        log_stdout(Path("/tmp/pg.log").read_text(encoding="utf-8", errors="replace"))
        raise RuntimeError("PostgreSQL did not accept connections within 30 seconds")

    _run(
        [pg_bin / "createdb", "-h", "127.0.0.1", "-p", PG_PORT, "-U", PG_USER, PG_DATABASE],
        cwd=root,
    )
    log_stdout(f"PostgreSQL is ready at {PG_TEST_URI}")


# ---------------------------------------------------------------------------
# The jobs
# ---------------------------------------------------------------------------


def _conventional_commits(root: Path) -> None:
    """Check every commit subject on the branch."""
    _section("Checking commit subjects")
    base = os.environ.get("REACTORCIDE_BASE_REF", "origin/main")
    _run(["git", "fetch", "--quiet", "origin", "main"], cwd=root, check=False)

    listed = _run(
        ["git", "log", "--format=%H %s", f"{base}..HEAD"],
        cwd=root,
        capture=True,
        check=False,
    )
    if listed.returncode != 0:
        log_stdout(f"Could not list commits against {base}; nothing to check")
        return

    bad: List[str] = []
    for line in listed.stdout.splitlines():
        if not line.strip():
            continue
        sha, _, subject = line.partition(" ")
        # A merge commit is generated, not written, so it is not held to the
        # convention.
        parents = _run(
            ["git", "rev-list", "--parents", "-n", "1", sha], cwd=root, capture=True
        )
        if len(parents.stdout.split()) > 2:
            log_stdout(f"  skip  {sha[:8]} (merge)")
            continue
        if CONVENTIONAL_SUBJECT.match(subject):
            log_stdout(f"  ok    {sha[:8]} {subject}")
        else:
            bad.append(f"{sha[:8]} {subject}")

    if bad:
        log_stdout("")
        log_stdout("These subjects are not Conventional Commits:")
        for entry in bad:
            log_stdout(f"  {entry}")
        raise RuntimeError("One or more commit subjects are not Conventional Commits")


def _lint(root: Path) -> None:
    """Everything ./tools.sh lint does, plus the formatting check.

    gofmt is checked here rather than in tools.sh because it is a CI
    concern: a developer's editor usually formats on save, and a build that
    fails on formatting locally is a nuisance rather than a help.
    """
    _ensure_go(root)
    _ensure_node(root)

    _section("gofmt")
    unformatted = _run(
        ["gofmt", "-l", "api", "coredb"], cwd=root, capture=True
    ).stdout.strip()
    if unformatted:
        log_stdout("These files are not gofmt-clean:")
        for name in unformatted.splitlines():
            log_stdout(f"  {name}")
        raise RuntimeError("Run gofmt -w on the files above")
    log_stdout("all Go files are formatted")

    _section("go vet (api)")
    _run(["go", "vet", "./..."], cwd=root / "api")
    _section("go vet (coredb)")
    _run(["go", "vet", "./..."], cwd=root / "coredb")

    _npm_install(root)
    _section("tsc --noEmit (webapp)")
    _run(["npm", "run", "typecheck"], cwd=root / "webapp")

    # This file, and the rest of the plugin.
    #
    # It runs only in CI, so nothing else would ever catch a name that does
    # not exist — and one did not: a refactor left `home` referenced in the
    # deploy job after the binding moved into a helper. Every check passed
    # and the job died in production, having already pushed the image.
    # `--select F` is pyflakes' rules, which is that class exactly.
    _section("ruff (.reactorcide/plugins)")
    uv = _ensure_uv(root)
    _run([str(uv), "tool", "run", "ruff", "check", "--select", "F", ".reactorcide/plugins"],
         cwd=root)


def _test_go(root: Path) -> None:
    """The Go suite on SQLite, with the race detector.

    SQLite needs nothing installed, so this is the fast lane: it fails on
    almost every real bug and finishes in a fraction of the Postgres job.
    """
    _ensure_go(root)
    _section("go test -race (api)")
    _run(["go", "test", "-race", "./..."], cwd=root / "api")
    _section("go test -race (coredb)")
    _run(["go", "test", "-race", "./..."], cwd=root / "coredb")


def _test_postgres(root: Path) -> None:
    """The same suite against the backend production uses.

    store/postgres and store/sqlite hold their own SQL, so a query is only
    exercised on the backend the tests happen to run on. This job is why
    both are covered.
    """
    _ensure_go(root)
    _start_postgres(root)
    _section("go test against PostgreSQL (api)")
    _run(
        ["go", "test", "-count=1", "./..."],
        cwd=root / "api",
        env={"TINKU_TEST_DB_URI": PG_TEST_URI},
    )


def _test_web(root: Path) -> None:
    _ensure_node(root)
    _npm_install(root)
    _section("npm test (webapp)")
    _run(["npm", "test"], cwd=root / "webapp")


def _website(root: Path) -> None:
    """Build the marketing site and check what it produced.

    PySocha exits 0 even when a template renders nothing useful, so the
    exit code alone proves very little.
    """
    uv = _ensure_uv(root)
    website = root / "website"

    _section("Building the site")
    _run(["./tools.sh", "build"], cwd=website)

    _section("Checking the output")
    required = [
        "site/index.html",
        "site/styles.css",
        "site/site.js",
        "site/assets/tinku-mark.svg",
    ]
    for name in required:
        path = website / name
        if not path.exists() or path.stat().st_size == 0:
            raise RuntimeError(f"{name} is missing or empty after the build")
        log_stdout(f"  ok  {name}")

    _section("Checking the community links")
    page = (website / "site/index.html").read_text(encoding="utf-8")
    for link in (
        "https://forgeutah.tech",
        "https://discord.gg/",
        "https://github.com/catalystcommunity/tinku",
    ):
        if link not in page:
            raise RuntimeError(f"site/index.html no longer links to {link}")
        log_stdout(f"  ok  {link}")

    # site/ is committed, so a change to site-src/ without a rebuild leaves
    # the deployed site stale. Catch it here rather than after a merge.
    _section("Checking that site/ matches site-src/")
    diff = _run(["git", "diff", "--stat", "--", "site"], cwd=website, capture=True)
    if diff.stdout.strip():
        log_stdout(diff.stdout)
        raise RuntimeError(
            "site/ is out of date. Run './tools.sh site build' and commit the result."
        )
    log_stdout("  ok  the committed site matches its source")
    log_stdout(f"  (built with {uv})")




# ---------------------------------------------------------------------------
# Releasing
# ---------------------------------------------------------------------------

# The tag semver-tags pushes, and the only place a release version comes
# from. The version file is written AFTER the tag, so a job that read the
# file would publish the previous number.
RELEASE_TAG = re.compile(r"^v(?P<version>(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*))$")

GITHUB_API = "https://api.github.com"


def _github(method: str, path: str, body: dict | None = None) -> dict:
    """One call to the GitHub API with the job's token."""
    token = _require("GITHUB_PAT")
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(GITHUB_API + path, data=data, method=method)
    request.add_header("Authorization", f"Bearer {token}")
    request.add_header("Accept", "application/vnd.github+json")
    if data is not None:
        request.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(request, timeout=60) as response:
        payload = response.read().decode()
    return json.loads(payload) if payload else {}


def _release_version(root: Path) -> tuple[str, str]:
    """The (tag, version) this release job is for.

    The runner puts the tag in REACTORCIDE_BRANCH for a tag_created workflow.
    Falling back to the tags on HEAD keeps `run-local` usable, and refusing
    when there is no single release tag is deliberate: a release job with no
    version has nothing correct to publish.
    """
    branch = os.environ.get("REACTORCIDE_BRANCH", "").strip()
    candidates = [branch] if branch else []
    if not candidates:
        listed = _run(["git", "tag", "--points-at", "HEAD"], cwd=root, capture=True, check=False)
        candidates = listed.stdout.split()

    matches = [c for c in candidates if RELEASE_TAG.fullmatch(c)]
    if len(matches) != 1:
        raise RuntimeError(
            f"a release job needs exactly one release tag, found {matches or 'none'}"
        )
    tag = matches[0]
    return tag, RELEASE_TAG.fullmatch(tag).group("version")


def _configure_git(root: Path) -> None:
    repository = _require("TINKU_REPOSITORY")
    token = _require("GITHUB_PAT")
    _run(["git", "config", "user.name",
          os.environ.get("GIT_USER_NAME", "Catalyst Community (automation)")], cwd=root)
    _run(["git", "config", "user.email",
          os.environ.get("GIT_USER_EMAIL", "catalystcommunityci@todandlorna.com")], cwd=root)
    # The URL carries the token, so this one command is not logged.
    result = subprocess.run(
        ["git", "remote", "set-url", "origin",
         f"https://x-access-token:{token}@github.com/{repository}.git"],
        cwd=root, check=False, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
    )
    if result.returncode != 0:
        raise RuntimeError("could not point the remote at the authenticated URL")
    log_stdout("+ git remote set-url origin (token not shown)")


def _release_tag(root: Path) -> None:
    """Work out the next version and push its tag.

    Three things have to be true before semver-tags can be trusted:
    the checkout is on the real main tip, the history is complete, and the
    tags are present. The runner prepares a merge ref with a shallow clone
    and no tags, so all three have to be arranged here — a shallow history
    silently produces the WRONG version rather than an error.
    """
    _configure_git(root)

    _section("Putting the checkout on main, with its history and tags")
    _run(["git", "fetch", "--unshallow", "origin"], cwd=root, check=False)
    _run(["git", "fetch", "--tags", "--prune", "--force", "origin",
          "+refs/heads/main:refs/remotes/origin/main"], cwd=root)
    _run(["git", "checkout", "-B", "main", "origin/main"], cwd=root)

    binary = _install_semver_tags()

    _section("Working out the next version")
    result = _run([str(binary), "run", "--output_json"], cwd=root, capture=True)
    metadata = _last_json_object(result.stdout)
    if metadata is None:
        raise RuntimeError("semver-tags returned no release metadata")

    published = str(metadata.get("New_release_published", "")).strip().lower() == "true"
    if not published:
        # Not a failure. A merge that carries no releasable change — docs, a
        # chore — is allowed to produce nothing.
        log_stdout("No releasable change in this merge; no tag was pushed.")
        return

    tag = str(metadata.get("New_release_git_tag", "")).strip()
    if not RELEASE_TAG.fullmatch(tag):
        raise RuntimeError(f"semver-tags produced a tag this repository does not use: {tag!r}")
    version = RELEASE_TAG.fullmatch(tag).group("version")
    log_stdout(f"Released {tag}")

    # semver-tags pushed the tag, which is what starts the release. This
    # commit records the number in the tree, so the repository states what
    # it last released.
    _section("Recording the version")
    _push_version(root, version)


# How many times to try the version push. More than one because a
# concurrent merge can advance main between the sync above and the push;
# that is a race, not a fault, and re-basing onto the new main fixes it.
PUSH_ATTEMPTS = 3


def _stamp_version(root: Path, version: str) -> bool:
    """Write the version and stage it. False when it is already recorded.

    Idempotent, so a retry can re-apply it after re-basing onto a main that
    moved.
    """
    (root / "version" / "VERSION.txt").write_text(f"{version}\n")
    _run(["git", "add", "version/VERSION.txt"], cwd=root)
    staged = _run(["git", "diff", "--cached", "--quiet"], cwd=root, check=False)
    return staged.returncode != 0


def _push_version(root: Path, version: str) -> None:
    """Commit the version and push it to main.

    Failure here is FATAL. The org's CI account is allowed past branch
    protection, so a refused push means something is actually wrong — the
    token, the grant, or the protection rule — and swallowing it would leave
    a repository whose recorded version silently stops matching its tags.
    The tag is already pushed at this point, so the failure is loud rather
    than damaging.
    """
    if not _stamp_version(root, version):
        log_stdout(f"main already records {version}")
        return
    _run(["git", "commit", "-m", f"ci: record version {version}"], cwd=root)

    for attempt in range(1, PUSH_ATTEMPTS + 1):
        pushed = _run(["git", "push", "origin", "HEAD:main"], cwd=root, check=False)
        if pushed.returncode == 0:
            log_stdout(f"main records {version}")
            return
        if attempt == PUSH_ATTEMPTS:
            raise RuntimeError(
                f"could not push the version commit to main after {PUSH_ATTEMPTS} attempts. "
                f"The tag for {version} is already pushed and its release is running, so "
                "this is a recording failure rather than a release failure — check the "
                "CI account's push permission on main."
            )

        # The re-base is tolerant on purpose. When the remote is
        # unreachable or the credential is wrong, the fetch fails too — and
        # raising THAT error would bury the useful one below, which names
        # what an operator should go and look at.
        log_stdout(f"main advanced; re-basing the version commit (attempt {attempt})")
        fetched = _run(["git", "fetch", "--tags", "--prune", "--force", "origin",
                        "+refs/heads/main:refs/remotes/origin/main"], cwd=root, check=False)
        if fetched.returncode != 0:
            raise RuntimeError(
                f"could not reach the remote to record version {version}. The tag is "
                "already pushed and its release is running, so this is a recording "
                "failure — check the CI account's credential and its push permission "
                "on main."
            )
        _run(["git", "reset", "--hard", "origin/main"], cwd=root)
        if not _stamp_version(root, version):
            log_stdout(f"a concurrent release already recorded {version}")
            return
        _run(["git", "commit", "-m", f"ci: record version {version}"], cwd=root)


def _install_semver_tags() -> Path:
    """Fetch the newest semver-tags release."""
    if shutil.which("semver-tags"):
        return Path(shutil.which("semver-tags"))

    latest = _github("GET", "/repos/catalystcommunity/semver-tags/releases/latest")
    url = next(
        (asset.get("browser_download_url") for asset in latest.get("assets", [])
         if isinstance(asset, dict) and asset.get("name") == "semver-tags.tar.gz"),
        None,
    )
    if not isinstance(url, str) or not url.startswith(
        "https://github.com/catalystcommunity/semver-tags/releases/download/"
    ):
        raise RuntimeError("the semver-tags release does not carry the expected archive")

    log_stdout(f"Installing semver-tags {latest.get('tag_name')}")
    archive = Path("/tmp/semver-tags.tar.gz")
    urllib.request.urlretrieve(url, archive)
    with tarfile.open(archive) as tar:
        tar.extractall("/tmp", filter="data")
    archive.unlink()
    binary = Path("/tmp/semver-tags")
    binary.chmod(0o755)
    return binary


def _last_json_object(output: str) -> dict | None:
    """semver-tags prints its result as the last JSON object on stdout."""
    for line in reversed(output.splitlines()):
        line = line.strip()
        if not line:
            continue
        try:
            value = json.loads(line)
        except json.JSONDecodeError:
            continue
        if isinstance(value, dict):
            return value
    return None


def _release_images(root: Path) -> None:
    """Build and publish the api and web client images for one release tag."""
    tag, version = _release_version(root)
    registry = _require("REGISTRY")
    prefix = _require("REGISTRY_PATH_PREFIX")

    _install_build_tools()
    _write_registry_auth(registry)
    _wait_for_buildkit(root)

    # Both images build from the REPOSITORY ROOT: the api image reaches the
    # sibling coredb module, and the webapp image reaches version/.
    for name, dockerfile in (("api", "api/Dockerfile"), ("webapp", "webapp/Dockerfile")):
        image = f"{registry}/{prefix}-{name}"
        _section(f"Building {image}:{version}")
        image_tar = Path(f"/tmp/tinku-{name}.tar")
        _run(
            [
                "buildctl", "build",
                "--frontend", "dockerfile.v0",
                "--local", "context=.",
                "--local", f"dockerfile={Path(dockerfile).parent}",
                "--opt", f"filename={Path(dockerfile).name}",
                "--output", f"type=docker,name={image}:{version},dest={image_tar}",
            ],
            cwd=root,
        )
        for published in (version, "latest"):
            _run(["crane", "push", str(image_tar), f"{image}:{published}"], cwd=root)
        image_tar.unlink()
        log_stdout(f"Published {image}:{version}")

    log_stdout(f"{tag}: images published")


def _release_github(root: Path) -> None:
    """Cut the GitHub release for this tag.

    It runs LAST, after the images and the deployment. A release that exists
    for artifacts which failed to build is a lie somebody has to go and
    undo.
    """
    tag, version = _release_version(root)
    repository = _require("TINKU_REPOSITORY")

    _section(f"Releasing {tag}")
    try:
        existing = _github("GET", f"/repos/{repository}/releases/tags/{tag}")
        if existing.get("id"):
            log_stdout(f"{tag} is already released; nothing to do")
            return
    except urllib.error.HTTPError as err:
        if err.code != 404:
            raise

    release = _github("POST", f"/repos/{repository}/releases", {
        "tag_name": tag,
        "name": f"tinku {version}",
        "generate_release_notes": True,
    })
    log_stdout(f"Released {release.get('html_url', tag)}")

# ---------------------------------------------------------------------------
# Deploying the marketing site
# ---------------------------------------------------------------------------

# Pinned. A moving chart version would change the deployment without
# anything in this repository changing.
HELM_CHART_VERSION = "1.1.1"
BUILDKIT_VERSION = "0.17.3"
CRANE_VERSION = "0.20.3"



def _install_build_tools() -> None:
    """buildctl and crane, which the runner image does not carry."""
    arch = platform.machine()
    if arch != "x86_64":
        raise RuntimeError(f"no pinned build tooling for {arch}")
    _install_tool(
        "buildctl",
        f"https://github.com/moby/buildkit/releases/download/v{BUILDKIT_VERSION}"
        f"/buildkit-v{BUILDKIT_VERSION}.linux-amd64.tar.gz",
        "bin/buildctl",
    )
    _install_tool(
        "crane",
        f"https://github.com/google/go-containerregistry/releases/download/v{CRANE_VERSION}"
        f"/go-containerregistry_Linux_x86_64.tar.gz",
        "crane",
    )


def _install_deploy_tools(root: Path) -> None:
    """helm and kubectl, for the jobs that touch the cluster."""
    home = Path(os.environ.get("HOME", "/root"))
    if not shutil.which("helm"):
        log_stdout("Installing helm")
        _run(
            ["sh", "-c",
             "curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash"],
            cwd=root,
            env={"USE_SUDO": "false", "HELM_INSTALL_DIR": str(home / ".local" / "bin")},
        )
    if not shutil.which("kubectl"):
        log_stdout("Installing kubectl")
        with urllib.request.urlopen("https://dl.k8s.io/release/stable.txt") as response:
            kubectl_version = response.read().decode().strip()
        kubectl = home / ".local" / "bin" / "kubectl"
        urllib.request.urlretrieve(
            f"https://dl.k8s.io/release/{kubectl_version}/bin/linux/amd64/kubectl", kubectl
        )
        kubectl.chmod(0o755)


def _write_registry_auth(registry: str) -> None:
    """Registry credentials, where buildctl and crane both look for them."""
    home = Path(os.environ.get("HOME", "/root"))
    docker_dir = home / ".docker"
    docker_dir.mkdir(parents=True, exist_ok=True)
    auth = base64.b64encode(
        f"{_require('REGISTRY_USER')}:{_require('REGISTRY_PASSWORD')}".encode()
    ).decode()
    (docker_dir / "config.json").write_text(json.dumps({"auths": {registry: {"auth": auth}}}))
    (docker_dir / "config.json").chmod(0o600)


def _wait_for_buildkit(root: Path) -> None:
    """The sidecar starts alongside the job, not before it."""
    _section("Waiting for the BuildKit sidecar")
    for _ in range(30):
        probe = _run(["buildctl", "debug", "info"], cwd=root, capture=True, check=False)
        if probe.returncode == 0:
            return
        time.sleep(1)
    raise RuntimeError("the BuildKit sidecar was not ready within 30 seconds")


def _install_tool(name: str, url: str, member: str | None) -> None:
    """Fetch one static binary into ~/.local/bin if it is not already there."""
    if shutil.which(name):
        return
    log_stdout(f"Installing {name}")
    home = Path(os.environ.get("HOME", "/root"))
    local_bin = home / ".local" / "bin"
    local_bin.mkdir(parents=True, exist_ok=True)
    os.environ["PATH"] = f"{local_bin}:{os.environ.get('PATH', '')}"

    archive = Path("/tmp") / f"{name}.tar.gz"
    urllib.request.urlretrieve(url, archive)
    with tarfile.open(archive) as tar:
        wanted = member or name
        extracted = tar.extractfile(wanted)
        if extracted is None:
            raise RuntimeError(f"{wanted} is not in the {name} archive")
        target = local_bin / name
        target.write_bytes(extracted.read())
        target.chmod(0o755)
    archive.unlink()


def _require(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise RuntimeError(f"{name} is not set; the deploy job needs it")
    return value


def _deploy_website(root: Path) -> None:
    """Build the site image, push it, and roll the Helm release.

    The shape tnl-site uses — the pysocha-site chart, one release per
    version — written in Python here so it reads like every other job in
    this repository.

    The version is the RELEASE TAG. It used to be a VERSION.txt of the
    site's own, which meant the repository had two version numbers that had
    to be remembered separately; now one tag releases everything.
    """
    website = root / "website"
    _tag, version = _release_version(root)

    # A placeholder domain would create a real ingress for a name nobody
    # owns. Refuse before anything is built.
    values = (website / "values.yaml").read_text()
    if "REPLACE-ME" in values:
        raise RuntimeError(
            "website/values.yaml still has the placeholder domain. "
            "Set a real one before deploying."
        )

    registry = _require("REGISTRY")
    registry_path = _require("REGISTRY_PATH")
    namespace = _require("K8S_NAMESPACE")
    release = _require("HELM_RELEASE")
    chart = _require("HELM_CHART")
    registry_user = _require("REGISTRY_USER")
    registry_password = _require("REGISTRY_PASSWORD")
    kubeconfig_content = _require("KUBECONFIG_CONTENT")
    image = f"{registry}/{registry_path}"

    _section(f"Deploying {image}:{version}")
    _install_build_tools()
    _install_deploy_tools(root)
    _write_registry_auth(registry)
    _wait_for_buildkit(root)

    _section("Building the image")
    image_tar = Path("/tmp/tinku-website.tar")
    _run(
        [
            "buildctl", "build",
            "--frontend", "dockerfile.v0",
            "--local", "context=.",
            "--local", "dockerfile=.",
            "--output", f"type=docker,name={image}:{version},dest={image_tar}",
        ],
        cwd=website,
    )

    _section("Pushing the image")
    # An internal registry on a bare address serves plain HTTP. The public
    # one does not, so the flag is a setting rather than a constant.
    push = ["crane", "push"]
    if os.environ.get("REGISTRY_INSECURE", "").strip().lower() == "true":
        push.append("--insecure")
    for tag in (version, "latest"):
        _run([*push, str(image_tar), f"{image}:{tag}"], cwd=root)
    image_tar.unlink()

    _section("Deploying")
    kube_dir = Path(os.environ.get("HOME", "/root")) / ".kube"
    kube_dir.mkdir(parents=True, exist_ok=True)
    kubeconfig = kube_dir / "config"
    kubeconfig.write_text(kubeconfig_content)
    kubeconfig.chmod(0o600)

    _run(
        ["helm", "repo", "add", "catalyst-helm",
         "https://raw.githubusercontent.com/catalystcommunity/charts/main"],
        cwd=root,
    )
    _run(["helm", "repo", "update"], cwd=root)

    # `kubectl apply` of a dry-run manifest, so both are idempotent.
    for manifest_args in (
        ["create", "namespace", namespace],
        [
            "create", "secret", "docker-registry", "regcred",
            "--namespace", namespace, "--save-config",
            f"--docker-server={registry}",
            f"--docker-username={registry_user}",
            f"--docker-password={registry_password}",
        ],
    ):
        rendered = _run(
            ["kubectl", *manifest_args, "--dry-run=client", "-o", "yaml"],
            cwd=root, capture=True,
        )
        apply = subprocess.run(
            ["kubectl", "apply", "-f", "-"],
            cwd=root, input=rendered.stdout, text=True,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False,
        )
        log_stdout(f"  {apply.stdout.strip()}")
        if apply.returncode != 0:
            raise RuntimeError(f"kubectl apply failed for {manifest_args[1]}")

    _run(
        [
            "helm", "upgrade", "--install", "--create-namespace",
            "--namespace", namespace, release, chart,
            "--version", HELM_CHART_VERSION,
            "--set", f"image.repository={image}",
            "--set", f"image.tag={version}",
            "--set", "imagePullSecrets[0].name=regcred",
            "-f", "values.yaml",
        ],
        cwd=website,
    )
    log_stdout(f"Deployed {release} {version} to {namespace}")


class TinkuJobsPlugin(Plugin):
    """Run the selected tinku job after runnerlib prepares the source."""

    def __init__(self) -> None:
        super().__init__(name="tinku_jobs", priority=100)

    def supported_phases(self) -> List[PluginPhase]:
        return [PluginPhase.POST_SOURCE_PREP]

    def execute(self, context: PluginContext) -> None:
        root = _repo_root(context)

        # git refuses to work in a directory somebody else owns, which is
        # what a checkout mounted into a container looks like.
        config_count = int(os.environ.get("GIT_CONFIG_COUNT", "0"))
        os.environ[f"GIT_CONFIG_KEY_{config_count}"] = "safe.directory"
        os.environ[f"GIT_CONFIG_VALUE_{config_count}"] = str(root)
        os.environ["GIT_CONFIG_COUNT"] = str(config_count + 1)

        job = os.environ.get("TINKU_JOB")
        if not job:
            raise RuntimeError("TINKU_JOB must select a runnerlib lifecycle job")

        log_stdout(f"tinku job: {job} (root {root})")

        if job == "conventional-commits":
            _conventional_commits(root)
        elif job == "lint":
            _lint(root)
        elif job == "test-go":
            _test_go(root)
        elif job == "test-postgres":
            _test_postgres(root)
        elif job == "test-web":
            _test_web(root)
        elif job == "website":
            _website(root)
        elif job == "deploy-website":
            _deploy_website(root)
        elif job == "release-tag":
            _release_tag(root)
        elif job == "release-images":
            _release_images(root)
        elif job == "release-github":
            _release_github(root)
        else:
            raise RuntimeError(f"Unknown TINKU_JOB value: {job}")

