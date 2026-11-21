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
