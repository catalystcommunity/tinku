package csilservices

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/catalystcommunity/tinku/api/internal/csil"
	"github.com/catalystcommunity/tinku/api/internal/store"
)

// SearchService implements csil.SearchService: one query across organizations,
// gatherings, events and series.
//
// It runs four independent searches and returns all four result sets rather
// than merging them into one ranked list. Merging would need a relevance
// score comparable across four different shapes, and a wrong ranking hides
// results more thoroughly than four honest lists do.
type SearchService struct {
	Store store.Store
	// OriginDomain is this instance's own name, which is what decides
	// whether a record's domain is foreign.
	OriginDomain string
	Now          func() time.Time
}

var _ csil.SearchService = (*SearchService)(nil)

// maxRadiusKM caps a proximity search. Beyond this the bounding box covers
// most of a hemisphere and the prefilter stops filtering anything.
const maxRadiusKM = 500.0

// earthRadiusKM is the mean radius, which is what a great-circle distance
// over a few hundred kilometres wants.
const earthRadiusKM = 6371.0

func (s *SearchService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *SearchService) Search(ctx context.Context, req csil.SearchRequest) (csil.SearchResults, error) {
	c := callerOf(ctx)
	now := s.now()

	// Fold the query here, in Go, to match the pre-folded search_text
	// columns — see searchText in text.go for why the folding is not done
	// in SQL.
	query := ""
	if req.Query != nil {
		query = strings.ToLower(strings.TrimSpace(*req.Query))
	}
	place, circle, err := placeFilterOf(req.Location)
	if err != nil {
		return csil.SearchResults{}, err
	}
	page := pageOf(req.Page)
	want := kindsWanted(req.Kinds)

	results := csil.SearchResults{
		Organizations: []csil.Organization{},
		Gatherings:    []csil.Gathering{},
		Events:        []csil.Event{},
		Series:        []csil.EventSeries{},
		RemoteEvents:  []csil.RemoteEvent{},
	}

	// An organization has no place and no time, so a query that constrains either
	// cannot match one. Saying so here beats returning every organization
	// alongside a properly filtered event list.
	constrainedByPlaceOrTime := !place.IsEmpty() || req.StartsAfter != nil || req.StartsBefore != nil ||
		boolValue(req.OnlineOnly) || boolValue(req.InPersonOnly)

	if want["organization"] && !constrainedByPlaceOrTime {
		organizations, total, err := s.Store.ListOrganizations(ctx, store.OrganizationFilter{Query: query, Page: page})
		if err != nil {
			return csil.SearchResults{}, err
		}
		for i := range organizations {
			role, onRoster, err := s.Store.OrganizationRoleFor(ctx, organizations[i].ID, c.ID)
			if err != nil {
				return csil.SearchResults{}, err
			}
			results.Organizations = append(results.Organizations, toOrganization(&organizations[i], organizationViewer(c, role, onRoster), s.OriginDomain))
		}
		results.OrganizationTotal = uint64(total)
	}

	if want["gathering"] {
		gatherings, total, err := s.Store.ListGatherings(ctx, store.GatheringFilter{
			Query: query, Place: place, Page: page,
		})
		if err != nil {
			return csil.SearchResults{}, err
		}
		for i := range gatherings {
			access, err := s.Store.GatheringAccessFor(ctx, gatherings[i].ID, c.ID)
			if err != nil {
				return csil.SearchResults{}, err
			}
			publish, err := publishDecisionFor(ctx, s.Store, &gatherings[i])
			if err != nil {
				return csil.SearchResults{}, err
			}
			results.Gatherings = append(results.Gatherings,
				toGathering(&gatherings[i], gatheringViewer(c, access), publish, s.OriginDomain))
		}
		results.GatheringTotal = uint64(total)
	}

	if want["event"] {
		f := store.EventFilter{
			Query:        query,
			Place:        place,
			OnlineOnly:   boolValue(req.OnlineOnly),
			InPersonOnly: boolValue(req.InPersonOnly),
			Now:          now,
			Page:         page,
		}
		if req.StartsAfter != nil {
			f.StartsAfter = *req.StartsAfter
		}
		if req.StartsBefore != nil {
			f.StartsBefore = *req.StartsBefore
		}
		if req.IncludeStarted != nil {
			f.IncludeStarted = *req.IncludeStarted
		}
		events, total, err := s.Store.ListEvents(ctx, f)
		if err != nil {
			return csil.SearchResults{}, err
		}
		// The SQL prefilter matched a box; a circle is what was asked for.
		events, dropped := refineToCircle(events, circle)
		out, err := (&EventService{Store: s.Store, OriginDomain: s.OriginDomain, Now: s.Now}).presentAll(ctx, c, events)
		if err != nil {
			return csil.SearchResults{}, err
		}
		results.Events = out
		results.EventTotal = uint64(total - dropped)
	}

	if want["series"] {
		series, total, err := s.Store.ListEventSeries(ctx, store.EventSeriesFilter{
			Query: query, Place: place, Page: page,
		})
		if err != nil {
			return csil.SearchResults{}, err
		}
		events := &EventService{Store: s.Store, OriginDomain: s.OriginDomain, Now: s.Now}
		for i := range series {
			viewer, err := events.seriesViewerFor(ctx, c, &series[i])
			if err != nil {
				return csil.SearchResults{}, err
			}
			results.Series = append(results.Series, toEventSeries(&series[i], viewer, s.OriginDomain))
		}
		results.SeriesTotal = uint64(total)
	}

	// What peers sent, when this instance is a directory. A separate list
	// rather than mixed into `events`, because a remote event is a summary
	// and a link: there is nothing here to attend and nobody here who can
	// edit it. An instance that does not federate holds none of these, so
	// the query costs one count that returns zero.
	if want["remote-event"] {
		f := store.RemoteEventFilter{
			Query:        query,
			Place:        place,
			OnlineOnly:   boolValue(req.OnlineOnly),
			InPersonOnly: boolValue(req.InPersonOnly),
			Now:          now,
			Page:         page,
		}
		if req.StartsAfter != nil {
			f.StartsAfter = *req.StartsAfter
		}
		if req.StartsBefore != nil {
			f.StartsBefore = *req.StartsBefore
		}
		if req.IncludeStarted != nil {
			f.IncludeStarted = *req.IncludeStarted
		}
		remote, total, err := s.Store.ListRemoteEvents(ctx, f)
		if err != nil {
			return csil.SearchResults{}, err
		}
		remote, dropped := refineRemoteToCircle(remote, circle)
		for i := range remote {
			results.RemoteEvents = append(results.RemoteEvents, toRemoteEvent(&remote[i]))
		}
		results.RemoteEventTotal = uint64(total - dropped)
	}

	return results, nil
}

// refineRemoteToCircle is refineToCircle for what peers sent. The two are
// separate because the row types are separate, and giving them a shared
// interface to save nine lines would put a method on a store row for the
// benefit of one caller.
func refineRemoteToCircle(events []store.RemoteEvent, circle *csil.GeoCircle) ([]store.RemoteEvent, int64) {
	if circle == nil {
		return events, 0
	}
	kept := events[:0]
	var dropped int64
	for i := range events {
		lat, lon := events[i].Location.Latitude, events[i].Location.Longitude
		if lat == nil || lon == nil ||
			haversineKM(circle.Latitude, circle.Longitude, *lat, *lon) > circle.RadiusKm {
			dropped++
			continue
		}
		kept = append(kept, events[i])
	}
	return kept, dropped
}

// kindsWanted turns the request's kind list into a lookup. An empty or
// absent list means every kind, so browsing needs no argument at all.
func kindsWanted(kinds []csil.SearchKind) map[string]bool {
	if len(kinds) == 0 {
		return map[string]bool{
			"organization": true, "gathering": true, "event": true,
			"series": true, "remote-event": true,
		}
	}
	want := map[string]bool{}
	for _, k := range kinds {
		want[string(k)] = true
	}
	return want
}

func boolValue(b *bool) bool { return b != nil && *b }

// placeFilterOf converts the wire filter into the store's, and returns the
// circle separately so the caller can refine the box afterwards.
func placeFilterOf(f *csil.LocationFilter) (store.PlaceFilter, *csil.GeoCircle, error) {
	var place store.PlaceFilter
	if f == nil {
		return place, nil, nil
	}
	if f.Locality != nil {
		place.Locality = strings.ToLower(strings.TrimSpace(*f.Locality))
	}
	if f.Region != nil {
		place.Region = strings.ToLower(strings.TrimSpace(*f.Region))
	}
	if f.Country != nil {
		place.Country = strings.ToLower(strings.TrimSpace(*f.Country))
	}
	if f.Near == nil {
		return place, nil, nil
	}

	circle := *f.Near
	if circle.Latitude < -90 || circle.Latitude > 90 {
		return place, nil, Invalid("location", "a latitude is between -90 and 90")
	}
	if circle.Longitude < -180 || circle.Longitude > 180 {
		return place, nil, Invalid("location", "a longitude is between -180 and 180")
	}
	if circle.RadiusKm <= 0 {
		return place, nil, Invalid("location", "a radius is greater than zero")
	}
	if circle.RadiusKm > maxRadiusKM {
		circle.RadiusKm = maxRadiusKM
	}
	place.Box = boundingBox(circle)
	return place, &circle, nil
}

// boundingBox is the smallest latitude/longitude rectangle containing the
// circle. It is a prefilter, not the answer: near a pole the longitude span
// widens without bound, so it is clamped to the whole range there and the
// circle refinement in Go does the real work.
func boundingBox(c csil.GeoCircle) *store.GeoBox {
	latDelta := c.RadiusKm / 111.0 // one degree of latitude is ~111 km everywhere

	box := store.GeoBox{
		MinLatitude:  math.Max(c.Latitude-latDelta, -90),
		MaxLatitude:  math.Min(c.Latitude+latDelta, 90),
		MinLongitude: -180,
		MaxLongitude: 180,
	}
	// A degree of longitude shrinks with the cosine of the latitude, and
	// reaches zero at the poles — where dividing by it would be nonsense
	// and the full range is the honest bound.
	if cos := math.Cos(c.Latitude * math.Pi / 180); cos > 0.01 {
		lonDelta := c.RadiusKm / (111.0 * cos)
		if lonDelta < 180 {
			box.MinLongitude = c.Longitude - lonDelta
			box.MaxLongitude = c.Longitude + lonDelta
		}
	}
	return &box
}

// refineToCircle drops the corners the bounding box let through. It also
// reports how many it dropped, so the total a client renders is not larger
// than the list it can page through.
//
// An event with no coordinates is dropped by a proximity search: "within 25
// km of here" cannot be true of something with no position, and keeping it
// would be answering a different question.
func refineToCircle(events []store.Event, circle *csil.GeoCircle) ([]store.Event, int64) {
	if circle == nil {
		return events, 0
	}
	kept := events[:0]
	var dropped int64
	for i := range events {
		lat, lon := events[i].Location.Latitude, events[i].Location.Longitude
		if lat == nil || lon == nil {
			dropped++
			continue
		}
		if haversineKM(circle.Latitude, circle.Longitude, *lat, *lon) > circle.RadiusKm {
			dropped++
			continue
		}
		kept = append(kept, events[i])
	}
	return kept, dropped
}

// haversineKM is the great-circle distance between two points, in
// kilometres. It lives in Go rather than SQL because SQLite has no
// trigonometry a query can rely on, and one implementation is what makes
// the two backends answer the same.
func haversineKM(lat1, lon1, lat2, lon2 float64) float64 {
	const toRad = math.Pi / 180
	dLat := (lat2 - lat1) * toRad
	dLon := (lon2 - lon1) * toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthRadiusKM * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
