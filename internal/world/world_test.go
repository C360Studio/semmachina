package world_test

import (
	"errors"
	"time"
)

// errorAs is errors.As with the tests' preferred call shape.
func errorAs(err error, target any) bool { return errors.As(err, target) }

// testStamp is the fixed import timestamp. Fixed rather than time.Now() so a
// produced triple's timestamp is an assertable value.
var testStamp = time.Date(2026, 7, 28, 9, 15, 30, 0, time.UTC)
