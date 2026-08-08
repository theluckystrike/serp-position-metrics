# serp-position-metrics

Go package for the arithmetic behind search-position reporting: impression-weighted
average position, position bucketing, click-through-rate curve interpolation,
projected clicks, share of voice, and snapshot-to-snapshot movement.

```
go get github.com/theluckystrike/serp-position-metrics
```

## What it does not do

It ships no click-through-rate data. A CTR curve is a property of a market and a
measurement window, not a constant, so every curve is supplied by the caller. The
package validates the curve you give it, interpolates linearly between the ranks you
provided, and clamps outside them rather than extrapolating a decay curve past its
last measured point.

There is no network access, no clock and no global state; it is standard library only.

## Example

```go
rows := []serpmetrics.Row{
    {Query: "dscr calculator", Position: 4.2, Impressions: 900, Clicks: 61},
    {Query: "dscr formula", Position: 17.8, Impressions: 400, Clicks: 3},
}

avg, err := serpmetrics.WeightedAveragePosition(rows) // 8.3846...
dist, err := serpmetrics.Distribute(rows)             // 1 in "4-10", 1 in "11-20"
share, err := dist.ImpressionShare()

curve := serpmetrics.Curve{1: 0.30, 2: 0.20, 10: 0.04} // your measured curve
projected, err := serpmetrics.ProjectedClicks(rows, curve)
```

`Movement(before, after)` compares two snapshots keyed by query and returns the
queries that moved (sorted by biggest gain first), the ones that entered, and the
ones that left.

## Notes on the definitions

* `WeightedAveragePosition` weights by impressions, so rows that were never seen do
  not drag the mean.
* Bucket edges (`1-3`, `4-10`, `11-20`, `21-50`, `51+`) are this package's own
  convention, not anybody's official definition. `BucketOfEdges` takes your own.
* `ProjectedClicks` is a model of the rows under a curve, not a measurement of them.
  Comparing it with the observed click total is a reasonable way to see how far the
  curve is from reality.

Factored out of the measurement layer of the site generation pipeline at
<https://toolsthatrank.com/>.

MIT licensed.
