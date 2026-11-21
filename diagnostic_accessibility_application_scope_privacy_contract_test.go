package perfuncted

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// Application-scoped failure evidence should identify the scope class without
// disclosing the application filter or sensitive selector strings to a
// diagnostic artifact. Scope labels must not silently fall back to "unknown".
func TestAccessibilityFailureDiagnosticClassifiesApplicationScopeWithoutIdentity(t *testing.T) {
	locator, _ := newLocatorWaitFixture(t, 1, false)
	locator.scope.application = accessibility.ApplicationFilter{Name: "secret-application-identity"}
	locator.selector = accessibility.Selector{
		Role: "button", Name: "secret-target-name", Text: "secret-text-value",
		Attributes: map[string]string{"private": "secret-attribute-value"},
	}
	artifact := summarizeAccessibilityFailure(&AccessibilityFailureEvidence{Locator: locator})
	if artifact.Locator == nil || artifact.Locator.Scope != "application" {
		t.Fatalf("application scope not classified: %+v", artifact.Locator)
	}
	if !artifact.Locator.HasName || !artifact.Locator.HasText || artifact.Locator.AttributeCount != 1 {
		t.Fatalf("application selector evidence not summarized: %+v", artifact.Locator)
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"secret-application-identity", "secret-target-name", "secret-text-value", "secret-attribute-value"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("application-scoped diagnostic leaked %q", private)
		}
	}
}
