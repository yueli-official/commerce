package runtime

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestDecodeWebhookMasterKey(t *testing.T) {
	key := bytes.Repeat([]byte{0x5a}, 32)
	for name, encoded := range map[string]string{
		"base64": base64.StdEncoding.EncodeToString(key),
		"hex":    hex.EncodeToString(key),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeWebhookMasterKey(encoded)
			if err != nil {
				t.Fatalf("DecodeWebhookMasterKey: %v", err)
			}
			if !bytes.Equal(got, key) {
				t.Fatalf("decoded key differs")
			}
		})
	}
}

func TestDecodeWebhookMasterKeyRejectsMissingAndWrongLength(t *testing.T) {
	for _, value := range []string{"", "not-an-encoding", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		if _, err := DecodeWebhookMasterKey(value); err == nil {
			t.Fatalf("DecodeWebhookMasterKey(%q) succeeded", value)
		}
	}
}
