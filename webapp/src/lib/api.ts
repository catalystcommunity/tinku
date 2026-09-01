// The single API client the app uses. Everything that talks to tinku-api
// goes through this — there is no `fetch` anywhere else in src/.
import { AsyncApiClient } from "~/gen/client.async.gen";
import { createHttpTransport } from "./httpTransport";

/** The app-wide client, over the real HTTP carrier. */
export const api = new AsyncApiClient(createHttpTransport());

/** Build a client over a supplied transport. Tests use this. */
export function createApi(transport = createHttpTransport()): AsyncApiClient {
  return new AsyncApiClient(transport);
}
