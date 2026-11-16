package catalog

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSignedManifestTrustAndTamper(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "team.pub"), []byte(base64.StdEncoding.EncodeToString(pub)), 0644)
	payload, _ := json.Marshal(Entry{ID: "nginx-test", Software: "nginx", Version: "1.0"})
	env := Envelope{KeyID: "team", Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))}
	var out Entry
	if err := Verify(env, dir, &out); err != nil || out.ID != "nginx-test" {
		t.Fatal(out, err)
	}
	env.Payload = base64.StdEncoding.EncodeToString([]byte(`{"id":"evil"}`))
	if err := Verify(env, dir, &out); err == nil {
		t.Fatal("tampered manifest accepted")
	}
	env.KeyID = "unknown"
	if err := Verify(env, dir, &out); err == nil {
		t.Fatal("unknown signer accepted")
	}
	env.KeyID = "../team"
	if err := Verify(env, dir, &out); err == nil {
		t.Fatal("signer path traversal accepted")
	}
}
func TestBuiltinCatalogHasPinnedArtifacts(t *testing.T) {
	for _, entry := range Entries() {
		if err := Validate(entry); err != nil {
			t.Fatalf("%s: %v", entry.ID, err)
		}
	}
}
