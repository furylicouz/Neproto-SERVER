package selfupdate

var stageProgress = map[string]int{
	"idle":        0,
	"checking":    5,
	"downloading": 15,
	"verifying":   45,
	"extracting":  55,
	"backing_up":  65,
	"installing":  75,
	"restarting":  95,
	"succeeded":   100,
	"failed":      100,
}

func ProgressForStage(stage string) (int, bool) {
	progress, ok := stageProgress[stage]
	return progress, ok
}
