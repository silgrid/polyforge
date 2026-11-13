package driver

// ids_test.go: the shipped lanes' step ids the tests name. The driver
// itself keys nothing on a step id; a test drives a
// shipped lane, or a test lane shaped like one, and names its steps.
const (
	releaseStepID      = "release"
	deployStepID       = "deploy"
	publishStepID      = "publish"
	developBuildStepID = "build"
	reviewStepID       = "review"
	verificationStepID = "verification"
	deliverStepID      = "deliver"
)
