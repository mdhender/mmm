// Copyright (c) 2026 Michael D Henderson.

package mmm

import (
	"github.com/maloquacious/semver"
)

func Version() semver.Version {
	return semver.Version{
		Major:      0,
		Minor:      20,
		Patch:      1,
		PreRelease: "beta",
		Build:      semver.Commit(),
	}
}
