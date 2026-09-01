// Wire naming, shared by every transport this app builds.
//
// csilgen's generated clients call the transport seam with the CSIL service
// name as declared — "AuthService", "GreetingService" — and the op already
// in its schema (kebab-case) spelling. What goes on the wire is the service
// name with a trailing "Service" stripped and lowercased, which is what
// tinku-api's dispatch table is keyed on
// (api/internal/server/dispatch.go).
//
// The api normalizes both spellings on the way in, so this conversion is not
// strictly required for tinku to work. It is here anyway so what leaves
// this app is the canonical form, and so a reader comparing a captured
// request against the schema sees the names the schema uses.

/** Convert a generated CSIL service name (GreetingService) to its wire key (greeting). */
export function serviceToWire(service: string): string {
  return service.replace(/Service$/, "").toLowerCase();
}

/**
 * Convert a PascalCase method name to its kebab-case op.
 *
 * The generated clients for tinku already pass kebab-case ops, so this is
 * a no-op for them. It exists because that is a property of the current
 * generator rather than of the protocol, and other hosts in this
 * organization (longhouse, firepit) do need the conversion — a generator
 * change should not become a wire break here.
 */
export function methodToOp(method: string): string {
  return method.replace(/([a-z0-9])([A-Z])/g, "$1-$2").toLowerCase();
}
