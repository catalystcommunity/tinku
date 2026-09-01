// Session state, shared across the app.
//
// The session itself is an httpOnly cookie the browser cannot read, so
// "who am I" is a question only the server can answer: this store asks
// auth.whoami and caches the reply. `undefined` means "not asked yet",
// `null` means "asked, and nobody is logged in" — a distinction the UI needs
// so it can avoid flashing a logged-out view while the first call is still
// in flight.
import { createContext, createSignal, onMount, useContext, type JSX } from "solid-js";
import type { UserProfile } from "~/gen/types.gen";
import { api } from "./api";
import { TinkuServiceError } from "./errors";

type SessionUser = UserProfile | null | undefined;

interface SessionValue {
  user: () => SessionUser;
  /** Re-ask the server who the caller is. */
  refresh: () => Promise<void>;
  /** Mint a session without an identity provider. Dev builds only. */
  devLogin: (handle: string, domain: string) => Promise<void>;
  logout: () => Promise<void>;
}

const SessionContext = createContext<SessionValue>();

export function SessionProvider(props: { children: JSX.Element }): JSX.Element {
  const [user, setUser] = createSignal<SessionUser>(undefined);

  const refresh = async () => {
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
  };

  const devLogin = async (handle: string, domain: string) => {
    setUser(await api.devAuth.devLogin({ handle, domain }));
  };

  const logout = async () => {
    await api.auth.logout({});
    setUser(null);
  };

  onMount(refresh);

  return (
    <SessionContext.Provider value={{ user, refresh, devLogin, logout }}>
      {props.children}
    </SessionContext.Provider>
  );
}

export function useSession(): SessionValue {
  const value = useContext(SessionContext);
  if (!value) {
    throw new Error("useSession was called outside a SessionProvider");
  }
  return value;
}
