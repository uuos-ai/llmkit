package llmkit

import "testing"

func TestAdapterManifestRequiresExplicitContract(t *testing.T) {
	manifest := AdapterManifest{ProviderID: "example", AdapterVersion: "1", Maturity: AdapterConformant, Operations: []Operation{OperationGenerate}, AuthSchemes: []AuthScheme{AuthBearer}}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	manifest.Maturity = "claimed"
	if err := manifest.Validate(); err == nil {
		t.Fatal("invalid maturity accepted")
	}
}
