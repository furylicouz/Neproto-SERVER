package cover

import "time"

type ProfileID uint8

const (
	ProfileQuiet ProfileID = iota + 1
	ProfileWeb
	ProfileInteractive
	ProfileStream
)

func (p ProfileID) String() string {
	switch p {
	case ProfileQuiet:
		return "quiet"
	case ProfileWeb:
		return "web"
	case ProfileInteractive:
		return "interactive"
	case ProfileStream:
		return "stream"
	default:
		return "unknown"
	}
}

type Limits struct {
	MaxRealDelay    time.Duration
	MaxPaddingBytes int
}

func ProfileLimits(profile ProfileID) Limits {
	definition, ok := profileDefinitions[profile]
	if !ok {
		return Limits{}
	}
	return definition.limits
}

type profileDefinition struct {
	limits     Limits
	buckets    []int
	dummySizes []int
}

var profileDefinitions = map[ProfileID]profileDefinition{
	ProfileQuiet: {
		limits: Limits{},
	},
	ProfileWeb: {
		limits: Limits{
			MaxRealDelay:    12 * time.Millisecond,
			MaxPaddingBytes: 8192,
		},
		buckets:    []int{128, 512, 1200, 4096, 16_384, 32_768, 49_152},
		dummySizes: []int{96, 256, 512, 1200},
	},
	ProfileInteractive: {
		limits: Limits{
			MaxRealDelay:    20 * time.Millisecond,
			MaxPaddingBytes: 1024,
		},
		buckets:    []int{96, 160, 256, 512, 1024, 1200, 4096},
		dummySizes: []int{96, 160, 256},
	},
	ProfileStream: {
		limits: Limits{
			MaxPaddingBytes: 256,
		},
		buckets: []int{1200, 4096, 16_384, 32_768, 49_152},
	},
}
