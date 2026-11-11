// UI tests that drive the app through a fake TRANSPORT rather than a fake
// API client: everything below the shell — the generated clients, the CBOR
// codec, the envelope encoding — is the real code, so a schema change that
// breaks the SPA breaks this test too.
import { screen, waitFor } from "@testing-library/dom";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AsyncServiceTransport } from "~/gen/client.async.gen";
import {
  toAttendeeListCbor,
  toEventCbor,
  toEventListCbor,
  toEventRoleListCbor,
  toEventSeriesListCbor,
  toGatheringCbor,
  toGatheringListCbor,
  toOrganizationListCbor,
  toServiceErrorCbor,
} from "~/gen/codec.gen";
import type { Event, Gathering, ViewerContext } from "~/gen/types.gen";
import { TinkuServiceError } from "~/lib/errors";
import { resetSession, useSession } from "~/lib/session";
import { routeLinks, startRouter } from "~/app/router";
import "./App";

vi.mock("~/lib/api", async () => {
  const { AsyncApiClient } = await import("~/gen/client.async.gen");
  return {
    api: new AsyncApiClient(fakeTransport()),
    createApi: () => new AsyncApiClient(fakeTransport()),
  };
});

/** A viewer with no powers, which is what an anonymous caller gets. */
const stranger: ViewerContext = {
  isAdmin: false,
  isOwner: false,
  isOrganizer: false,
  isPresenter: false,
  isMember: false,
  hasJoined: false,
  canEdit: false,
  canDelete: false,
  canManageMembers: false,
};

const gathering: Gathering = {
  id: "01J00000000000000000000A",
  slug: "thursday-bouldering",
  originDomain: "tinku.test",
  name: "Thursday Bouldering",
  blurb: "We climb on Thursdays.",
  description: "",
  owners: [
    {
      kind: "user",
      id: "01J00000000000000000000B",
      displayName: "Ada",
      handle: "ada",
      originDomain: "example.test",
    },
  ],
  publishEvents: "unset",
  origin: { domain: "tinku.test", isExternal: false },
  // The server resolves the three-level rule and sends the answer; a
  // fixture therefore states an answer rather than the inputs to one.
  publish: { publishing: false, source: "instance", canOverride: true },
  memberCount: 4,
  eventCount: 1,
  nextEventAt: new Date("2026-02-12T02:00:00Z"),
  viewer: stranger,
  createdAt: new Date("2026-01-01T12:00:00Z"),
  updatedAt: new Date("2026-01-01T12:00:00Z"),
};

/** An event that has NOT started: it carries its description. */
const upcoming: Event = {
  id: "01J00000000000000000000C",
  gatheringId: gathering.id,
  title: "Second Thursday Session",
  description: "Bring shoes.",
  isOnline: false,
  isInPerson: true,
  location: {
    name: "Ada Lovelace Gym",
    address: "1 Main St",
    locality: "Denver",
    region: "Colorado",
    postalCode: "80202",
    country: "US",
  },
  startsAt: new Date("2026-02-12T02:00:00Z"),
  endsAt: new Date("2026-02-12T04:00:00Z"),
  timezone: "America/Denver",
  origin: { domain: "tinku.test", isExternal: false },
  locked: false,
  attendeeCount: 3,
  viewerAttending: false,
  viewer: stranger,
  createdAt: new Date("2026-01-01T12:00:00Z"),
  updatedAt: new Date("2026-01-01T12:00:00Z"),
};

/**
 * An event that HAS started. The server withholds the description — the
 * field is simply absent — and sets `locked`. This fixture is the wire
 * shape, so the test proves the UI handles what the server really sends.
 */
const started: Event = {
  ...upcoming,
  id: "01J00000000000000000000D",
  title: "January Session",
  description: undefined,
  startsAt: new Date("2026-01-08T02:00:00Z"),
  endsAt: new Date("2026-01-08T04:00:00Z"),
  locked: true,
};

function fakeTransport(): AsyncServiceTransport {
  return {
    async call(service: string, op: string): Promise<Uint8Array> {
      switch (`${service}/${op}`) {
        case "AuthService/whoami":
          // Nobody is logged in. The transport signals a declared error arm
          // the way the real one does — by throwing the same type, so
          // session.ts's "unauthenticated is expected" branch is the one
          // that runs.
          throw new TinkuServiceError({ code: 2, message: "no active session" });
        case "GatheringService/list-gatherings":
          return toGatheringListCbor({ gatherings: [gathering], total: 1 });
        case "GatheringService/get-gathering":
          return toGatheringCbor(gathering);
        case "EventService/list-events":
          return toEventListCbor({ events: [upcoming, started], total: 2 });
        case "EventService/list-event-series":
          return toEventSeriesListCbor({ series: [], total: 0 });
        case "EventService/get-event":
          return toEventCbor(started);
        case "EventService/list-attendees":
          return toAttendeeListCbor({ eventId: started.id, attendees: [], total: 0 });
        case "EventService/list-event-roles":
          return toEventRoleListCbor({ roles: [] });
        case "OrganizationService/list-organizations":
          return toOrganizationListCbor({ organizations: [], total: 0 });
        default:
          return toServiceErrorCbor({ code: 1, message: `unexpected call ${service}/${op}` });
      }
    },
  };
}

let stopLinks: (() => void) | undefined;

// The session answer is app state; a test must not inherit the last one.
afterEach(() => resetSession());

/** Boot the app as index.ts does, into the test document. */
function renderApp() {
  stopLinks?.();
  startRouter();
  stopLinks = routeLinks();
  void useSession().refresh();
  document.body.append(document.createElement("tinku-app"));
}

describe("the app shell", () => {
  it("offers a skip link and marks the current page for assistive technology", async () => {
    renderApp();

    // The skip link is the first thing in the tab order, and it is a real
    // link to the main landmark rather than a scroll handler.
    const skip = screen.getByRole("link", { name: "Skip to main content" });
    expect(skip).toHaveAttribute("href", "#main");

    // The current location is conveyed by aria-current, not only by colour.
    const discover = screen.getByRole("link", { name: "Discover" });
    expect(discover).toHaveAttribute("aria-current", "page");
    expect(screen.getByRole("link", { name: "Gatherings" })).not.toHaveAttribute("aria-current");
  });

  it("gives an anonymous caller the sign-in control and no session identity", async () => {
    renderApp();
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Sign in" })).toBeInTheDocument(),
    );
    expect(screen.queryByRole("button", { name: "Sign out" })).not.toBeInTheDocument();
    // The disclosure state is on the button that takes the focus, not on
    // the push-button host, which a screen reader does not read.
    expect(screen.getByRole("button", { name: "Sign in" })).toHaveAttribute("aria-expanded", "false");
  });
});

describe("browsing gatherings", () => {
  it("lists gatherings with their federated address and membership count", async () => {
    const user = userEvent.setup();
    renderApp();

    await user.click(screen.getByRole("link", { name: "Gatherings" }));

    await waitFor(() =>
      expect(screen.getByRole("link", { name: "Thursday Bouldering" })).toBeInTheDocument(),
    );
    expect(screen.getByText("thursday-bouldering@tinku.test")).toBeInTheDocument();
    // The plural is chosen for the count, not hard-coded.
    expect(screen.getByText(/4 members/)).toBeInTheDocument();

    // An anonymous caller gets no create form: the whole form is absent
    // rather than present and disabled.
    expect(screen.queryByRole("button", { name: "Create" })).not.toBeInTheDocument();
  });
});

describe("a started event", () => {
  it("says it has started, explains why, and shows no description", async () => {
    const user = userEvent.setup();
    renderApp();

    await user.click(screen.getByRole("link", { name: "Gatherings" }));
    await waitFor(() =>
      expect(screen.getByRole("link", { name: "Thursday Bouldering" })).toBeInTheDocument(),
    );
    await user.click(screen.getByRole("link", { name: "Thursday Bouldering" }));

    await waitFor(() =>
      expect(screen.getByRole("link", { name: "January Session" })).toBeInTheDocument(),
    );
    await user.click(screen.getByRole("link", { name: "January Session" }));

    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "January Session" })).toBeInTheDocument(),
    );
    // The lock is stated in words, not left to be inferred from missing
    // controls.
    expect(screen.getByText(/This event has started/)).toBeInTheDocument();
    expect(screen.getByText(/no longer shown/)).toBeInTheDocument();
    expect(screen.queryByText("Bring shoes.")).not.toBeInTheDocument();
    // Attendance is closed, so neither control is offered.
    expect(screen.queryByRole("button", { name: "I am attending" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "I am not attending" })).not.toBeInTheDocument();
  });
});
