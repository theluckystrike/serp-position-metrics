package serpmetrics

import (
	"errors"
	"math"
	"testing"
)

func approx(t *testing.T, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("got %g, want %g (tolerance %g)", got, want, tol)
	}
}

func TestRowValid(t *testing.T) {
	cases := []struct {
		name string
		row  Row
		ok   bool
	}{
		{"ok", Row{Query: "a", Position: 1, Impressions: 10, Clicks: 1}, true},
		{"position zero", Row{Query: "a", Position: 0, Impressions: 10}, false},
		{"negative impressions", Row{Query: "a", Position: 2, Impressions: -1}, false},
		{"negative clicks", Row{Query: "a", Position: 2, Impressions: 5, Clicks: -1}, false},
		{"clicks above impressions", Row{Query: "a", Position: 2, Impressions: 5, Clicks: 6}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.row.Valid()
			if c.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !c.ok && err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

func TestWeightedAveragePosition(t *testing.T) {
	rows := []Row{
		{Query: "a", Position: 2, Impressions: 900},
		{Query: "b", Position: 20, Impressions: 100},
	}
	// (2*900 + 20*100) / 1000 = 3.8
	got, err := WeightedAveragePosition(rows)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, got, 3.8, 1e-9)
}

func TestWeightedAveragePositionIgnoresZeroImpressionRows(t *testing.T) {
	withGhost := []Row{
		{Query: "a", Position: 2, Impressions: 900},
		{Query: "b", Position: 20, Impressions: 100},
		{Query: "ghost", Position: 90, Impressions: 0},
	}
	got, err := WeightedAveragePosition(withGhost)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, got, 3.8, 1e-9)
}

func TestWeightedAveragePositionNoImpressions(t *testing.T) {
	_, err := WeightedAveragePosition([]Row{{Query: "a", Position: 4}})
	if !errors.Is(err, ErrNoImpressions) {
		t.Fatalf("want ErrNoImpressions, got %v", err)
	}
}

func TestCTR(t *testing.T) {
	got, err := CTR([]Row{
		{Query: "a", Position: 1, Impressions: 200, Clicks: 40},
		{Query: "b", Position: 9, Impressions: 300, Clicks: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, got, 50.0/500.0, 1e-12)
}

func TestBucketOf(t *testing.T) {
	cases := []struct {
		pos  float64
		want Bucket
	}{
		{1, Top3},
		{3, Top3},
		{3.4, PageOne},
		{10, PageOne},
		{10.1, PageTwo},
		{20, PageTwo},
		{21, Deep},
		{50, Deep},
		{50.5, Tail},
		{99, Tail},
	}
	for _, c := range cases {
		if got := BucketOf(c.pos); got != c.want {
			t.Fatalf("BucketOf(%g) = %v, want %v", c.pos, got, c.want)
		}
	}
}

func TestBucketStrings(t *testing.T) {
	want := []string{"1-3", "4-10", "11-20", "21-50", "51+"}
	for i, w := range want {
		if got := Bucket(i).String(); got != w {
			t.Fatalf("Bucket(%d).String() = %q, want %q", i, got, w)
		}
	}
}

func TestBucketOfEdgesRejectsUnsorted(t *testing.T) {
	if _, err := BucketOfEdges(5, []float64{10, 3}); err == nil {
		t.Fatal("expected an error for unsorted edges")
	}
	if _, err := BucketOfEdges(0.5, []float64{3, 10}); err == nil {
		t.Fatal("expected an error for a position below rank 1")
	}
}

func TestBucketOfEdgesCustom(t *testing.T) {
	got, err := BucketOfEdges(7, []float64{5})
	if err != nil {
		t.Fatal(err)
	}
	if got != Bucket(1) {
		t.Fatalf("got %d, want the open-ended band", int(got))
	}
}

func TestDistributeAndShare(t *testing.T) {
	rows := []Row{
		{Query: "a", Position: 2, Impressions: 600, Clicks: 60},
		{Query: "b", Position: 8, Impressions: 300, Clicks: 9},
		{Query: "c", Position: 40, Impressions: 100, Clicks: 0},
	}
	d, err := Distribute(rows)
	if err != nil {
		t.Fatal(err)
	}
	if d.Queries[Top3] != 1 || d.Queries[PageOne] != 1 || d.Queries[Deep] != 1 {
		t.Fatalf("unexpected query counts: %v", d.Queries)
	}
	if d.Clicks[Top3] != 60 {
		t.Fatalf("clicks in Top3 = %d, want 60", d.Clicks[Top3])
	}
	share, err := d.ImpressionShare()
	if err != nil {
		t.Fatal(err)
	}
	approx(t, share[Top3], 0.6, 1e-12)
	approx(t, share[Deep], 0.1, 1e-12)
	sum := 0.0
	for _, v := range share {
		sum += v
	}
	approx(t, sum, 1, 1e-12)
}

func TestImpressionShareNoImpressions(t *testing.T) {
	d, err := Distribute([]Row{{Query: "a", Position: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ImpressionShare(); !errors.Is(err, ErrNoImpressions) {
		t.Fatalf("want ErrNoImpressions, got %v", err)
	}
}

func TestCurveValidate(t *testing.T) {
	if err := (Curve{}).Validate(); err == nil {
		t.Fatal("expected an error for an empty curve")
	}
	if err := (Curve{0: 0.3}).Validate(); err == nil {
		t.Fatal("expected an error for rank 0")
	}
	if err := (Curve{1: 1.4}).Validate(); err == nil {
		t.Fatal("expected an error for a rate above 1")
	}
	if err := (Curve{1: 0.3, 2: 0.2}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCurveMonotonic(t *testing.T) {
	if !(Curve{1: 0.3, 2: 0.2, 3: 0.2}).Monotonic() {
		t.Fatal("a non-increasing curve should be monotonic")
	}
	if (Curve{1: 0.2, 2: 0.3}).Monotonic() {
		t.Fatal("a rising curve should not be monotonic")
	}
}

func TestCurveInterpolate(t *testing.T) {
	c := Curve{1: 0.30, 2: 0.20, 10: 0.04}
	cases := []struct {
		pos  float64
		want float64
	}{
		{1, 0.30},
		{1.5, 0.25},
		{2, 0.20},
		{6, 0.20 + 0.5*(0.04-0.20)},
		{10, 0.04},
		{25, 0.04}, // clamped, not extrapolated
	}
	for _, tc := range cases {
		got, err := c.Interpolate(tc.pos)
		if err != nil {
			t.Fatal(err)
		}
		approx(t, got, tc.want, 1e-12)
	}
	if _, err := c.Interpolate(0.4); err == nil {
		t.Fatal("expected an error below rank 1")
	}
}

func TestProjectedClicksAndShareOfVoice(t *testing.T) {
	c := Curve{1: 0.30, 10: 0.03}
	mine := []Row{{Query: "a", Position: 1, Impressions: 1000}}
	rival := []Row{{Query: "a", Position: 10, Impressions: 1000}}
	market := append(append([]Row{}, mine...), Row{Query: "rival", Position: 10, Impressions: 1000})

	pc, err := ProjectedClicks(mine, c)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, pc, 300, 1e-9)

	pr, err := ProjectedClicks(rival, c)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, pr, 30, 1e-9)

	sov, err := ShareOfVoice(mine, market, c)
	if err != nil {
		t.Fatal(err)
	}
	approx(t, sov, 300.0/330.0, 1e-12)
}

func TestShareOfVoiceZeroMarket(t *testing.T) {
	c := Curve{1: 0.3}
	if _, err := ShareOfVoice(
		[]Row{{Query: "a", Position: 1, Impressions: 10}},
		[]Row{{Query: "b", Position: 1, Impressions: 0}},
		c,
	); err == nil {
		t.Fatal("expected an error when the market projects to zero clicks")
	}
}

func TestMovement(t *testing.T) {
	before := []Row{
		{Query: "up", Position: 12, Impressions: 100},
		{Query: "down", Position: 4, Impressions: 100},
		{Query: "gone", Position: 30, Impressions: 10},
	}
	after := []Row{
		{Query: "up", Position: 5, Impressions: 140},
		{Query: "down", Position: 9, Impressions: 80},
		{Query: "new", Position: 22, Impressions: 20},
	}
	moved, entered, left, err := Movement(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 2 || len(entered) != 1 || len(left) != 1 {
		t.Fatalf("moved=%d entered=%d left=%d", len(moved), len(entered), len(left))
	}
	if moved[0].Query != "up" {
		t.Fatalf("biggest gain should sort first, got %q", moved[0].Query)
	}
	approx(t, moved[0].Delta, 7, 1e-12)
	approx(t, moved[1].Delta, -5, 1e-12)
	if entered[0].Query != "new" || left[0].Query != "gone" {
		t.Fatalf("entered=%v left=%v", entered, left)
	}
}

func TestMovementRejectsDuplicateQuery(t *testing.T) {
	dup := []Row{
		{Query: "a", Position: 3, Impressions: 5},
		{Query: "a", Position: 4, Impressions: 5},
	}
	if _, _, _, err := Movement(dup, nil); err == nil {
		t.Fatal("expected an error for a duplicated query")
	}
}
