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
# Deploying the marketing site
# ---------------------------------------------------------------------------

# Pinned. A moving chart version would change the deployment without
# anything in this repository changing.
HELM_CHART_VERSION = "1.1.1"
BUILDKIT_VERSION = "0.17.3"
CRANE_VERSION = "0.20.3"


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

    The same shape tnl-site uses — internal registry, the pysocha-site
    chart — written here in Python rather than shell so it reads the same
    way as every other job in this repository.
    """
    website = root / "website"
    version = (website / "VERSION.txt").read_text().strip()
    if not version:
        raise RuntimeError("website/VERSION.txt is empty")

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

    home = Path(os.environ.get("HOME", "/root"))
    arch = platform.machine()
    if arch != "x86_64":
        raise RuntimeError(f"The deploy job has no pinned tooling for {arch}")

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

    # Registry auth for buildctl and crane.
    docker_dir = home / ".docker"
    docker_dir.mkdir(parents=True, exist_ok=True)
    auth = base64.b64encode(
        f"{registry_user}:{registry_password}".encode()
    ).decode()
    (docker_dir / "config.json").write_text(
        json.dumps({"auths": {registry: {"auth": auth}}})
    )
    (docker_dir / "config.json").chmod(0o600)

    _section("Waiting for the BuildKit sidecar")
    for _ in range(30):
        probe = _run(["buildctl", "debug", "info"], cwd=root, capture=True, check=False)
        if probe.returncode == 0:
            break
        time.sleep(1)
    else:
        raise RuntimeError("The BuildKit sidecar was not ready within 30 seconds")

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
    kube_dir = home / ".kube"
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
        else:
            raise RuntimeError(f"Unknown TINKU_JOB value: {job}")

