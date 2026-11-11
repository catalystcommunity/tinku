// Session state, shared across the app.
//
// The session itself is an httpOnly cookie the browser cannot read, so
// "who am I" is a question only the server can answer: this module asks
// auth.whoami and keeps the reply in the store (collection SESSION).
// `undefined` means "not asked yet", `null` means "asked, and nobody is
// logged in" — a distinction the UI needs so it can avoid flashing a
// logged-out view while the first call is still in flight.
//
// An element that reads `user()` lists SESSION in its `static watches`.
import type { UserProfile } from "~/gen/types.gen";
import { cell } from "~/state/store";
import { api } from "~/lib/api";
import { TinkuServiceError } from "./errors";

export type SessionUser = UserProfile | null | undefined;

/** The store collection of this module. */
export const SESSION = "session";

const userCell = cell<{ user: SessionUser }>(SESSION, "current", { user: undefined });

export interface SessionValue {
  user: () => SessionUser;
  /** Ask the server again who the caller is. */
  refresh: () => Promise<void>;
  /** Start a linkkeys sign-in: the browser goes to the identity domain. */
  beginLogin: (domain: string) => Promise<void>;
  /** Mint a session without an identity provider. Dev builds only. */
  devLogin: (handle: string, domain: string) => Promise<void>;
  logout: () => Promise<void>;
}

const user = () => userCell.get().user;
const setUser = (next: SessionUser) => userCell.set({ user: next });

async function refresh(): Promise<void> {
  try {
    setUser(await api.auth.whoami({}));
  } catch (e) {
    // An unauthenticated whoami is the expected answer for a caller with
    // no cookie, not a failure worth surfacing. Anything else is real and
    // is left visible in the console rather than swallowed.
    if (e instanceof TinkuServiceError && e.isUnauthenticated) {
      setUser(null);
      return;
    }
    console.error("whoami failed", e);
    setUser(null);
  }
}

async function beginLogin(domain: string): Promise<void> {
  const { redirectUrl } = await api.auth.beginLogin({ domain });
  window.location.assign(redirectUrl);
}

async function devLogin(handle: string, domain: string): Promise<void> {
  setUser(await api.devAuth.devLogin({ handle, domain }));
}

async function logout(): Promise<void> {
  await api.auth.logout({});
  setUser(null);
}

/** Forget the cached answer. Tests call it between cases. */
export function resetSession(): void {
  setUser(undefined);
}

const value: SessionValue = { user, refresh, beginLogin, devLogin, logout };

export function useSession(): SessionValue {
  return value;
}
