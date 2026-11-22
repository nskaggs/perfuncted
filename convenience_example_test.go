package perfuncted_test

import "github.com/nskaggs/perfuncted"

func ExampleOpenDefault() {
	s, err := perfuncted.OpenDefault()
	if err != nil {
		return
	}
	defer s.Close()
	_ = s.Do(perfuncted.Type("Invoice {A} + tax"), perfuncted.Key("tab"), perfuncted.Type("12.34"))
}

// ExampleWindowTarget_Do compiles the opt-in window-targeted scripting path.
// A real session requires a managed desktop with observable window identity.
func ExampleWindowTarget_Do() {
	s, err := perfuncted.OpenDefault(perfuncted.Require(perfuncted.CapabilityWindows))
	if err != nil {
		return
	}
	defer s.Close()
	_ = s.Window("Editor").Do(perfuncted.Type("Hello"), perfuncted.Key("tab"))
}
