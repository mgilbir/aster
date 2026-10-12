//go:build !unix

package main

// peakRSSMB is 0 where getrusage does not exist; the report shows it as such.
func peakRSSMB() float64 { return 0 }
