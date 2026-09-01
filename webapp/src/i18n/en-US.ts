// The en-US message catalog. It is the ONLY place an English string that a
// person reads is written; every other file names a key.
//
// Rules that keep a catalog translatable:
//
//   * No string is assembled by concatenation. A sentence with a value in
//     it is one message with a {placeholder}, because word order is not the
//     same in every language and a translator cannot reorder fragments a
//     component glued together.
//   * Plurals get an explicit `_one` / `_other` pair. English needs two
//     forms; a language that needs more gets more keys in its own catalog,
//     and `plural()` picks with Intl.PluralRules rather than `n === 1`.
//   * Nothing here is formatted. Dates, times and numbers go through Intl
//     with the active locale, so a catalog never contains a date format.
//   * Keys are namespaced by screen or concept, so a translator working on
//     one screen can see all of it at once.

export const enUS = {
  "app.name": "Tinku",
  "app.tagline": "Gatherings, in the open.",
  "app.skipToContent": "Skip to main content",
  "app.mainLabel": "Main content",
  "app.primaryNavLabel": "Primary",

  "nav.discover": "Discover",
  "nav.mine": "Mine",

  "session.signIn": "Sign in",
  "session.signOut": "Sign out",
  "session.signedInAs": "Signed in as {address}",
  "session.devSignIn": "Sign in (development)",
  "session.devSignInHint":
    "This build has development sign-in switched on. Enter a handle to get a session with no identity provider.",
  "session.handleLabel": "Handle",
  "session.domainLabel": "Domain",
  "session.working": "Signing in…",

  "common.loading": "Loading…",
  "common.nothingHere": "Nothing here yet.",
  "common.save": "Save",
  "common.saving": "Saving…",
  "common.cancel": "Cancel",
  "common.create": "Create",
  "common.creating": "Creating…",
  "common.delete": "Delete",
  "common.edit": "Edit",
  "common.back": "Back",
  "common.retry": "Try again",
  "common.required": "required",
  "common.optional": "optional",
  "common.showing": "Showing {shown} of {total}",
  "common.more": "Show more",
  "common.confirmDelete": "Delete {name}? This cannot be undone.",

  "error.unauthenticated": "Sign in to do that.",
  "error.forbidden": "You are not allowed to do that.",
  "error.notFound": "That is not here.",
  "error.invalid": "That input is not valid.",
  "error.unavailable": "Something this needs is not available right now.",
  "error.transport": "Could not reach the server.",
  "error.unknown": "Something went wrong.",

  "discover.title": "Discover",
  "discover.searchLabel": "Search",
  "discover.searchPlaceholder": "Climbing, book club, Denver…",
  "discover.searchAction": "Search",
  "discover.placeLegend": "Where",
  "discover.localityLabel": "City",
  "discover.regionLabel": "State or region",
  "discover.timeLegend": "When",
  "discover.fromLabel": "From",
  "discover.toLabel": "To",
  "discover.onlineOnly": "Online only",
  "discover.inPersonOnly": "In person only",
  "discover.includeStarted": "Include events that have started",
  "discover.resultsLabel": "Search results",
  "discover.noResults": "Nothing matched that search.",
  "discover.resultsAnnounced_one": "{count} result",
  "discover.resultsAnnounced_other": "{count} results",

  // "Organizations" in full, "Orgs" where a label has to be short — a
  // navigation item, a chip. Two keys rather than one truncated at render
  // time, because which abbreviation reads well is a per-language choice.
  //
  // The short form is a DISPLAY choice and lives only here. Routes, keys
  // and identifiers all spell the object out.
  "organization.plural": "Organizations",
  "organization.pluralShort": "Orgs",
  "organization.singular": "Organization",
  "organization.new": "Start an organization",
  "organization.nameLabel": "Name",
  "organization.blurbLabel": "Blurb",
  "organization.blurbHint": "A short statement of what this organization is. At most 300 words.",
  "organization.blurbCount_one": "{count} word of 300",
  "organization.blurbCount_other": "{count} words of 300",
  "organization.descriptionLabel": "Description",
  "organization.descriptionHint": "The long form. At most 10000 characters.",
  "organization.members_one": "{count} member",
  "organization.members_other": "{count} members",
  "organization.owners_one": "{count} owner",
  "organization.owners_other": "{count} owners",
  "organization.roster": "Roster",
  "organization.roleOwner": "Owner",
  "organization.roleMember": "Member",
  "organization.addMemberLegend": "Add somebody",
  "organization.addMemberAddress": "Address",
  "organization.addMemberAddressHint": "handle@domain",
  "organization.addMemberAction": "Add",
  "organization.removeMember": "Remove {name}",
  "organization.deleteNeedsAdmin": "Only an administrator can delete an organization.",
  "organization.emptyList": "No organizations yet. Start one.",
  "organization.mine": "My organizations",

  "gathering.plural": "Gatherings",
  "gathering.singular": "Gathering",
  "gathering.new": "Start a gathering",
  "gathering.nameLabel": "Name",
  "gathering.blurbLabel": "Blurb",
  "gathering.descriptionLabel": "Description",
  "gathering.ownedByLegend": "Owned by",
  "gathering.ownedByMe": "Me",
  "gathering.ownedByOrg": "An organization I own",
  "gathering.owners": "Owners",
  "gathering.members_one": "{count} member",
  "gathering.members_other": "{count} members",
  "gathering.events_one": "{count} listing",
  "gathering.events_other": "{count} listings",
  "gathering.join": "Join",
  "gathering.joining": "Joining…",
  "gathering.leave": "Leave",
  "gathering.leaving": "Leaving…",
  "gathering.joined": "You have joined this gathering.",
  "gathering.left": "You have left this gathering.",
  "gathering.nextEvent": "Next: {when}",
  "gathering.nothingScheduled": "Nothing scheduled.",
  "gathering.emptyList": "No gatherings yet. Start one.",
  "gathering.mine": "My gatherings",
  "gathering.schedule": "Schedule",
  "gathering.addOwnerLegend": "Add an owner",
  "gathering.roster": "Members",

  "event.plural": "Events",
  "event.singular": "Event",
  "event.new": "Schedule a single event",
  "event.newSeries": "Schedule a recurring event",
  "event.titleLabel": "Title",
  "event.descriptionLabel": "Description",
  "event.startsAtLabel": "Starts",
  "event.endsAtLabel": "Ends",
  "event.timezoneLabel": "Timezone",
  "event.timezoneHint": "The zone the time above is read in, not the reader's zone.",
  "event.resolvesTo": "That is {utc}, which is {local} where you are.",
  "event.onlineLabel": "Online",
  "event.inPersonLabel": "In person",
  "event.onlineUrlLabel": "Link",
  "event.locationLegend": "Location",
  "event.locationNameLabel": "Venue",
  "event.addressLabel": "Address",
  "event.localityLabel": "City",
  "event.regionLabel": "State or region",
  "event.postalCodeLabel": "Postal code",
  "event.countryLabel": "Country",
  "event.latitudeLabel": "Latitude",
  "event.longitudeLabel": "Longitude",
  "event.inPersonNeedsLocation": "An in-person event needs a location.",
  "event.needsOneMode": "An event is online, in person, or both.",
  "event.attend": "I am attending",
  "event.attending": "You are attending",
  "event.unattend": "I am not attending",
  "event.attendees_one": "{count} attending",
  "event.attendees_other": "{count} attending",
  "event.attendeeList": "Who is attending",
  "event.joinToAttend": "Join this gathering to mark attendance.",
  "event.started": "This event has started",
  "event.startedExplanation":
    "It can no longer be changed, attendance is closed, and its description is no longer shown.",
  "event.descriptionWithheld": "The description is not available once an event has started.",
  "event.emptyList": "Nothing scheduled.",
  "event.upcoming": "Upcoming",
  "event.online": "Online",
  "event.inPerson": "In person",
  "event.onlineAndInPerson": "Online and in person",
  "event.partOfSeries": "Part of a recurring event",
  "event.mine": "Events I am attending",
  "event.occurrences_one": "{count} occurrence scheduled",
  "event.occurrences_other": "{count} occurrences scheduled",
  "event.expand": "Look further ahead",

  "roles.organizer": "Organizer",
  "roles.presenter": "Presenter",
  "roles.owner": "Owner",
  "roles.member": "Member",
  "roles.admin": "Administrator",
  "roles.legend": "Roles",
  "roles.assign": "Assign a role",
  "roles.remove": "Remove {name} as {role}",
  "roles.presenterNote": "A presenter is billed as presenting and has a member's permissions.",

  // Recurrence is described from the structured rule, never from a string
  // the server sent, so every locale can put the parts in its own order.
  "recurrence.weekly": "Every {weekday}",
  "recurrence.weeklyInterval": "Every {interval} weeks on {weekday}",
  "recurrence.monthly": "The {ordinal} {weekday} of every month",
  "recurrence.monthlyInterval": "The {ordinal} {weekday} of every {interval} months",
  "recurrence.quarterly": "The {ordinal} {weekday} of every quarter",
  "recurrence.yearly": "The {ordinal} {weekday} of every year",
  "recurrence.dayOfMonth": "The {day} of every month",
  "recurrence.at": "{rule}, at {time}",
  "recurrence.ordinal.1": "first",
  "recurrence.ordinal.2": "second",
  "recurrence.ordinal.3": "third",
  "recurrence.ordinal.4": "fourth",
  "recurrence.ordinal.5": "fifth",
  "recurrence.ordinal.-1": "last",
  "recurrence.freqLabel": "Repeats",
  "recurrence.freq.weekly": "Weekly",
  "recurrence.freq.monthly": "Monthly",
  "recurrence.freq.quarterly": "Quarterly",
  "recurrence.freq.yearly": "Yearly",
  "recurrence.ordinalLabel": "Which one",
  "recurrence.weekdayLabel": "Day",
  "recurrence.intervalLabel": "Every",
  "recurrence.startTimeLabel": "Starts at",
  "recurrence.durationLabel": "Runs for (minutes)",
  "recurrence.startsOnLabel": "First possible date",
  "recurrence.endsOnLabel": "Last possible date",

  "weekday.monday": "Monday",
  "weekday.tuesday": "Tuesday",
  "weekday.wednesday": "Wednesday",
  "weekday.thursday": "Thursday",
  "weekday.friday": "Friday",
  "weekday.saturday": "Saturday",
  "weekday.sunday": "Sunday",

  "federation.title": "Federation",
  "federation.nav": "Federation",
  "federation.identity": "This instance is {address}",
  "federation.identityUnset": "This instance does not federate.",
  "federation.algorithm": "Signing: {algorithm}",
  "federation.unsignedWarning":
    "Deliveries are not authenticated. This signing scheme is for development only.",
  "federation.peers": "Peers",
  "federation.noPeers": "No peers yet.",
  "federation.addPeer": "Add a directory",
  "federation.addressLabel": "Address",
  "federation.addressHint": "handle@domain",
  "federation.baseUrlLabel": "Base URL",
  "federation.noteLabel": "Note",
  "federation.inbound": "Accepts from them",
  "federation.outbound": "Publishes to them",
  "federation.status.none": "No",
  "federation.status.pending": "Waiting",
  "federation.status.approved": "Yes",
  "federation.status.blocked": "Blocked",
  "federation.approve": "Approve",
  "federation.block": "Block",
  "federation.revoke": "Revoke",
  "federation.suspended": "Delivery stopped",
  "federation.suspendedSince": "Stopped {when}",
  "federation.lastFailure": "Last failure: {reason}",
  "federation.lastSuccess": "Last delivered {when}",
  "federation.queued_one": "{count} delivery waiting",
  "federation.queued_other": "{count} deliveries waiting",
  "federation.resume": "Restart delivery",
  "federation.remove": "Forget this peer",
  // One whole sentence per action, naming the peer it acts on. Two things
  // depend on this: a screen reader lists a page's buttons out of context,
  // so "Approve" on a page with five peers names none of them; and a
  // sentence assembled from "Accepts from them" + ": " + "Approve" is word
  // order this catalogue cannot change for another language.
  "federation.approveInbound": "Accept deliveries from {peer}",
  "federation.blockInbound": "Block deliveries from {peer}",
  "federation.approveOutbound": "Publish to {peer}",
  "federation.revokeOutbound": "Stop publishing to {peer}",
  "federation.resumePeer": "Restart delivery to {peer}",
  "federation.removePeer": "Forget the peer {peer}",
  "federation.setRateLimitFor": "Set the allowance for {peer}",
  "federation.remoteEvents": "From other domains",
  "federation.fromPeer": "From {address}",
  "federation.viewAtOrigin": "Open at its origin",

  "publish.legend": "Publishing to other domains",
  "publish.label": "Publish these events",
  "publish.unset": "Follow the level above",
  "publish.in": "Yes",
  "publish.out": "No",
  "publish.resolvedIn": "These events are published to peer directories.",
  "publish.resolvedOut": "These events are not published to other domains.",
  "publish.decidedByInstance": "Decided by this instance.",
  "publish.decidedByOrganization": "Decided by the owning organization.",
  "publish.decidedByGathering": "Decided here.",
  "publish.cannotOverride": "This instance does not allow this to be changed here.",

  "settings.title": "Instance settings",
  "settings.publishDefault": "Publish events by default",
  "settings.organizationOverride": "Organizations may change it",
  "settings.gatheringOverride": "Gatherings may change it",
  "settings.retentionDays": "Keep what peers send for (days)",
  "settings.retentionHint": "Counted from when an event ends. Zero keeps everything.",
  "settings.rateLimit": "Events a minute from one peer",
  "settings.rateLimitHint": "Zero means no limit. A peer over the limit has the rest of its batch refused.",
  "settings.save": "Save settings",
  "settings.saved": "Settings saved.",

  "federation.rateLimit": "Allowance",
  "federation.rateLimitInstance": "instance limit",
  "federation.rateLimitOwn": "{count} a minute",
  "federation.rateLimitedTotal_one": "{count} event refused for going too fast",
  "federation.rateLimitedTotal_other": "{count} events refused for going too fast",
  "federation.setRateLimit": "Set allowance",
  "federation.clearRateLimit": "Use the instance limit",

  "origin.external": "Another domain",
  "origin.at": "at {domain}",
  "origin.viaPeer": "via {peer}",
  "origin.externalWarning":
    "This belongs to {domain}, not to this site. A name here can match a local one and be a different thing.",

  "settings.originRateLimit": "Events a minute from one organization",
  "settings.originRateLimitHint":
    "Set below the peer limit, or it never applies. It stops one organization inside a peer using the whole allowance.",
  "volume.setOriginLimit": "Allowance for this organization",
  "volume.originLimitInstance": "instance limit",
  "volume.originLimited_one": "{count} refused from this organization",
  "volume.originLimited_other": "{count} refused from this organization",

  "volume.title": "Where deliveries come from",
  "volume.hint":
    "The limit applies to a peer. This says which organization inside a peer is sending the volume.",
  "volume.none": "Nothing has been delivered yet.",
  "volume.noOrganization": "(no organization named)",
  "volume.held_one": "{count} event held",
  "volume.held_other": "{count} events held",
  "volume.acceptedTotal_one": "{count} accepted in all",
  "volume.acceptedTotal_other": "{count} accepted in all",
  "volume.thisMinute": "{count} this minute of {limit}",
  "volume.thisMinuteNoLimit": "{count} this minute, no limit",
  "volume.lastReceived": "Last delivery {when}",
  "volume.peerThrottled": "This peer is over its limit.",
  "volume.peerSuspended": "Delivery from this peer has stopped.",

  "crash.title": "This page could not be shown",
  "crash.explain":
    "Something went wrong while drawing this screen. The rest of the site still works.",
  "crash.retry": "Try again",
  "crash.home": "Go to Discover",
  "crash.detail": "Details",

  "mine.title": "Mine",
  "mine.signInFirst": "Sign in to see what is yours.",
} as const;

/** Every key the app may ask for. A missing key is a type error, not a bug
 *  a person finds in production. */
export type MessageKey = keyof typeof enUS;

/**
 * The base key of a plural message. A plural lives in the catalog as
 * `key_one` and `key_other`, and `plural()` is called with the base — so
 * the base is a type of its own, derived from the catalog rather than
 * maintained beside it.
 */
export type PluralKey = MessageKey extends infer K
  ? K extends `${infer Base}_one`
    ? Base
    : never
  : never;

/** The shape a translation of this catalog must have. A locale that has not
 *  translated every key supplies what it has; the rest falls back to en-US. */
export type Catalog = Partial<Record<MessageKey, string>>;
