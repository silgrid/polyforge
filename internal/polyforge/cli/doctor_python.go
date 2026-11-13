package cli

// doctor_python.go: the `python3` row — the interpreter a
// workflow's step scripts run under resolves and is at least MinPython,
// judged by the rule the daemon applies at its start (python.go:
// `[daemon] python`, else python3 on this shell's PATH). A fail here is a
// daemon that refuses to start, or step scripts that cannot run.

const docChPython = "python3"

// pythonCheck judges the step scripts' interpreter: pass naming its path,
// version and source; fail naming why and the remedy.
func pythonCheck(configured string, lookPath func(string) (string, error), probe func(string) (string, error)) doctorCheck {
	in, err := ResolvePython(configured, lookPath, probe)
	if err != nil {
		return doctorCheck{Name: docChPython, Status: "fail",
			Detail: err.Error() + " — the daemon refuses to start without it (PF_PYTHON, the workflow step scripts' interpreter)"}
	}
	return doctorCheck{Name: docChPython, Status: "pass",
		Detail: "Python " + in.Version + " at " + in.Path + " (" + in.Source + "; the step scripts need >= " + MinPythonString() + ")"}
}
