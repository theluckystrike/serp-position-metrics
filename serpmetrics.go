// Package serpmetrics implements the arithmetic behind search-position
// reporting: impression-weighted average position, position bucketing,
// click-through-rate curve interpolation, projected clicks and share of
// voice, and snapshot-to-snapshot movement.
//
// The package ships no click-through-rate data of its own. Every curve is
// supplied by the caller, because a CTR curve is a property of a market and
// a measurement window, not a universal constant. Curve validates what it is
// given and Interpolate is a plain linear interpolation between the ranks the
// caller provided; nothing is estimated on the caller's behalf. If you need a
// curve to hand to Curve and have none of your own, click-through rate
// measured per position band across 34 Search Console properties, with the
// impression and click counts behind every band, is published at
// https://toolsthatrank.com/ctr-by-position/
//
// Everything here is pure arithmetic over values you already measured. No
// network access, no clock, no global state, standard library only.
//
// One caveat about the input rather than the arithmetic:
// WeightedAveragePosition and ShareOfVoice weight by the impression counts in
// the rows you supply, and a Search Console query export does not carry every
// impression a page received - rare searches are withheld, so the query rows
// for a page can sum to well under its page-level impression total. A
// measurement of how large that gap typically is, taken from the same
// properties, is at
// https://toolsthatrank.com/search-console-hidden-queries/
//
// Typical input is a row per query from an analytics export:
//
//	rows := []serpmetrics.Row{
//		{Query: "dscr calculator", Position: 4.2, Impressions: 900, Clicks: 61},
//		{Query: "dscr formula", Position: 17.8, Impressions: 400, Clicks: 3},
//	}
//	avg, _ := serpmetrics.WeightedAveragePosition(rows)
//
// Written for the measurement step of the site generation pipeline at
// https://toolsthatrank.com/ and kept as its own module because the maths is
// useful without the rest of it.
package serpmetrics

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// ErrNoImpressions is returned by aggregations that divide by an impression
// total when that total is zero.
var ErrNoImpressions = errors.New("serpmetrics: rows carry no impressions")

// Row is one measured query: where it ranked, how often it was seen, and how
// often it was clicked. Position is the average position over the reporting
// window and is therefore fractional; ranks start at 1.
type Row struct {
	Query       string
	Position    float64
	Impressions int
	Clicks      int
}

// Valid reports whether r can take part in an aggregation. A row is valid
// when its position is at least 1, its impressions are not negative, and its
// clicks neither are negative nor exceed its impressions.
func (r Row) Valid() error {
	if r.Position < 1 {
		return fmt.Errorf("serpmetrics: %q: position %g is below rank 1", r.Query, r.Position)
	}
	if r.Impressions < 0 {
		return fmt.Errorf("serpmetrics: %q: negative impressions %d", r.Query, r.Impressions)
	}
	if r.Clicks < 0 {
		return fmt.Errorf("serpmetrics: %q: negative clicks %d", r.Query, r.Clicks)
	}
	if r.Clicks > r.Impressions {
		return fmt.Errorf("serpmetrics: %q: %d clicks exceed %d impressions", r.Query, r.Clicks, r.Impressions)
	}
	return nil
}

// Validate checks every row and returns the first problem found.
func Validate(rows []Row) error {
	for _, r := range rows {
		if err := r.Valid(); err != nil {
			return err
		}
	}
	return nil
}

// TotalImpressions sums the impressions of rows.
func TotalImpressions(rows []Row) int {
	n := 0
	for _, r := range rows {
		n += r.Impressions
	}
	return n
}

// TotalClicks sums the clicks of rows.
func TotalClicks(rows []Row) int {
	n := 0
	for _, r := range rows {
		n += r.Clicks
	}
	return n
}

// WeightedAveragePosition returns the impression-weighted mean position of
// rows: sum(position*impressions)/sum(impressions). Rows with no impressions
// contribute nothing and do not drag the mean, which is the difference
// between this and a plain mean of the position column.
//
// It returns ErrNoImpressions when the impression total is zero.
func WeightedAveragePosition(rows []Row) (float64, error) {
	if err := Validate(rows); err != nil {
		return 0, err
	}
	total := TotalImpressions(rows)
	if total == 0 {
		return 0, ErrNoImpressions
	}
	sum := 0.0
	for _, r := range rows {
		sum += r.Position * float64(r.Impressions)
	}
	return sum / float64(total), nil
}

// CTR returns clicks divided by impressions across rows, in the range [0,1].
// It returns ErrNoImpressions when the impression total is zero.
func CTR(rows []Row) (float64, error) {
	if err := Validate(rows); err != nil {
		return 0, err
	}
	total := TotalImpressions(rows)
	if total == 0 {
		return 0, ErrNoImpressions
	}
	return float64(TotalClicks(rows)) / float64(total), nil
}

// Bucket is a coarse position band. The band edges are this package's own
// convention, chosen so that the first band is the part of a result page a
// reader reaches without scrolling and each later band is roughly a page
// further away. They are not anybody's official definition; if you need
// different edges, use BucketOfEdges.
type Bucket int

// The buckets, in rank order.
const (
	// Top3 covers positions 1 to 3 inclusive.
	Top3 Bucket = iota
	// PageOne covers positions above 3 up to and including 10.
	PageOne
	// PageTwo covers positions above 10 up to and including 20.
	PageTwo
	// Deep covers positions above 20 up to and including 50.
	Deep
	// Tail covers everything beyond position 50.
	Tail
)

// DefaultBucketEdges are the upper bounds, inclusive, used by BucketOf. The
// final bucket has no upper bound and so has no entry here.
var DefaultBucketEdges = []float64{3, 10, 20, 50}

// String renders the bucket as a short label such as "1-3" or "51+".
func (b Bucket) String() string {
	switch b {
	case Top3:
		return "1-3"
	case PageOne:
		return "4-10"
	case PageTwo:
		return "11-20"
	case Deep:
		return "21-50"
	case Tail:
		return "51+"
	}
	return fmt.Sprintf("Bucket(%d)", int(b))
}

// BucketOf places a position in a Bucket using DefaultBucketEdges. A
// fractional position falls in the bucket its value lies in, so 3.4 is
// PageOne rather than Top3.
func BucketOf(position float64) Bucket {
	b, _ := BucketOfEdges(position, DefaultBucketEdges)
	return b
}

// BucketOfEdges places a position using caller-supplied inclusive upper
// bounds, which must be strictly increasing. The returned index is the number
// of edges the position exceeds, so len(edges) means the open-ended last
// band. It reports an error for an unsorted edge list or a position below
// rank 1.
func BucketOfEdges(position float64, edges []float64) (Bucket, error) {
	if position < 1 {
		return 0, fmt.Errorf("serpmetrics: position %g is below rank 1", position)
	}
	for i := 1; i < len(edges); i++ {
		if edges[i] <= edges[i-1] {
			return 0, fmt.Errorf("serpmetrics: bucket edges are not strictly increasing at index %d", i)
		}
	}
	for i, e := range edges {
		if position <= e {
			return Bucket(i), nil
		}
	}
	return Bucket(len(edges)), nil
}

// Distribution is a count and an impression total per bucket.
type Distribution struct {
	Queries     map[Bucket]int
	Impressions map[Bucket]int
	Clicks      map[Bucket]int
}

// Distribute groups rows into the default buckets.
func Distribute(rows []Row) (Distribution, error) {
	if err := Validate(rows); err != nil {
		return Distribution{}, err
	}
	d := Distribution{
		Queries:     map[Bucket]int{},
		Impressions: map[Bucket]int{},
		Clicks:      map[Bucket]int{},
	}
	for _, r := range rows {
		b := BucketOf(r.Position)
		d.Queries[b]++
		d.Impressions[b] += r.Impressions
		d.Clicks[b] += r.Clicks
	}
	return d, nil
}

// ImpressionShare returns each bucket's share of total impressions, summing
// to 1 over the buckets present. It returns ErrNoImpressions when the total
// is zero.
func (d Distribution) ImpressionShare() (map[Bucket]float64, error) {
	total := 0
	for _, n := range d.Impressions {
		total += n
	}
	if total == 0 {
		return nil, ErrNoImpressions
	}
	out := make(map[Bucket]float64, len(d.Impressions))
	for b, n := range d.Impressions {
		out[b] = float64(n) / float64(total)
	}
	return out, nil
}

// Curve maps an integer rank to a click-through rate in [0,1]. The caller
// owns the numbers: measure them from your own data, or take them from a
// study you can cite. This package neither ships nor invents a curve.
type Curve map[int]float64

// Validate reports the first rank or rate in c that cannot be used: a rank
// below 1, or a rate outside [0,1]. An empty curve is invalid because nothing
// can be interpolated from it.
func (c Curve) Validate() error {
	if len(c) == 0 {
		return errors.New("serpmetrics: curve is empty")
	}
	for _, rank := range c.Ranks() {
		if rank < 1 {
			return fmt.Errorf("serpmetrics: curve rank %d is below 1", rank)
		}
		v := c[rank]
		if math.IsNaN(v) || v < 0 || v > 1 {
			return fmt.Errorf("serpmetrics: curve rate %g at rank %d is outside [0,1]", v, rank)
		}
	}
	return nil
}

// Ranks returns the ranks in c in ascending order.
func (c Curve) Ranks() []int {
	ranks := make([]int, 0, len(c))
	for r := range c {
		ranks = append(ranks, r)
	}
	sort.Ints(ranks)
	return ranks
}

// Monotonic reports whether the curve never rises as rank increases, which is
// the shape a click-through curve is normally expected to have. A curve that
// is not monotonic is not rejected anywhere in this package; the check exists
// so you can notice it.
func (c Curve) Monotonic() bool {
	ranks := c.Ranks()
	for i := 1; i < len(ranks); i++ {
		if c[ranks[i]] > c[ranks[i-1]] {
			return false
		}
	}
	return true
}

// Interpolate returns the rate at a possibly fractional position. Between two
// known ranks it interpolates linearly. Below the lowest known rank it
// returns that rank's rate, and above the highest known rank it returns that
// rank's rate; the curve is clamped rather than extrapolated, because
// extrapolating a decay curve past its last measured point invents data.
func (c Curve) Interpolate(position float64) (float64, error) {
	if err := c.Validate(); err != nil {
		return 0, err
	}
	if position < 1 {
		return 0, fmt.Errorf("serpmetrics: position %g is below rank 1", position)
	}
	ranks := c.Ranks()
	first, last := ranks[0], ranks[len(ranks)-1]
	if position <= float64(first) {
		return c[first], nil
	}
	if position >= float64(last) {
		return c[last], nil
	}
	lo := first
	for _, r := range ranks {
		if float64(r) <= position {
			lo = r
			continue
		}
		hi := r
		span := float64(hi - lo)
		t := (position - float64(lo)) / span
		return c[lo] + t*(c[hi]-c[lo]), nil
	}
	return c[last], nil
}

// ProjectedClicks estimates the clicks rows would earn at their measured
// positions under curve c: sum(impressions * c.Interpolate(position)). It is
// a model of the rows, not a measurement of them; compare it against
// TotalClicks to see how far the curve is from the observed behaviour.
func ProjectedClicks(rows []Row, c Curve) (float64, error) {
	if err := Validate(rows); err != nil {
		return 0, err
	}
	if err := c.Validate(); err != nil {
		return 0, err
	}
	total := 0.0
	for _, r := range rows {
		rate, err := c.Interpolate(r.Position)
		if err != nil {
			return 0, err
		}
		total += float64(r.Impressions) * rate
	}
	return total, nil
}

// ShareOfVoice returns mine's projected clicks as a fraction of the projected
// clicks of every competitor in market, under one shared curve. Include your
// own rows in market as well if you want your share of the whole market
// rather than your share of everyone else.
//
// It returns an error when the market projects to zero clicks, since a share
// of nothing is undefined.
func ShareOfVoice(mine, market []Row, c Curve) (float64, error) {
	mineClicks, err := ProjectedClicks(mine, c)
	if err != nil {
		return 0, err
	}
	marketClicks, err := ProjectedClicks(market, c)
	if err != nil {
		return 0, err
	}
	if marketClicks == 0 {
		return 0, errors.New("serpmetrics: market projects to zero clicks")
	}
	return mineClicks / marketClicks, nil
}

// Move is one query's change between two snapshots. Before and After are the
// positions in the earlier and later snapshot. Delta is Before-After, so a
// positive Delta means the query moved towards rank 1.
type Move struct {
	Query  string
	Before float64
	After  float64
	Delta  float64
}

// Movement compares two snapshots keyed by query. Queries in both snapshots
// are returned as moved, sorted by descending Delta so the biggest
// improvements come first. Queries only in after are returned as entered,
// queries only in before as left. Duplicate queries within one snapshot are
// an error, because there is no defined way to compare a query with itself.
func Movement(before, after []Row) (moved []Move, entered, left []Row, err error) {
	if err = Validate(before); err != nil {
		return nil, nil, nil, err
	}
	if err = Validate(after); err != nil {
		return nil, nil, nil, err
	}
	beforeIdx, err := index(before)
	if err != nil {
		return nil, nil, nil, err
	}
	afterIdx, err := index(after)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, r := range after {
		b, ok := beforeIdx[r.Query]
		if !ok {
			entered = append(entered, r)
			continue
		}
		moved = append(moved, Move{
			Query:  r.Query,
			Before: b.Position,
			After:  r.Position,
			Delta:  b.Position - r.Position,
		})
	}
	for _, r := range before {
		if _, ok := afterIdx[r.Query]; !ok {
			left = append(left, r)
		}
	}
	sort.SliceStable(moved, func(i, j int) bool { return moved[i].Delta > moved[j].Delta })
	return moved, entered, left, nil
}

func index(rows []Row) (map[string]Row, error) {
	m := make(map[string]Row, len(rows))
	for _, r := range rows {
		if _, dup := m[r.Query]; dup {
			return nil, fmt.Errorf("serpmetrics: query %q appears twice in one snapshot", r.Query)
		}
		m[r.Query] = r
	}
	return m, nil
}
