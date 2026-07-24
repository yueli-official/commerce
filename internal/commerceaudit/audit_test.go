package commerceaudit_test

import (
	"testing"

	"github.com/yueli-official/foundation/go/audit"

	"platform/services/commerce/internal/commerceaudit"
)

func TestDefinitionCompilesRecoveryActions(t *testing.T) {
	catalog, err := audit.Compile(commerceaudit.Definition())
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []commerceaudit.Action{
		commerceaudit.ActionAccessRevocationQueued,
		commerceaudit.ActionRemoteGrantRevoked,
		commerceaudit.ActionRecoveryRetried,
	} {
		if _, err := audit.BindAction(
			catalog,
			audit.Action{Name: audit.ActionName(action), Version: 1},
			func(commerceaudit.Evidence) []audit.EvidenceField {
				return []audit.EvidenceField{
					audit.Code("commerce.recovery_state", "revoke_pending"),
					audit.Reference("commerce.provider_grant", "grant-1"),
					audit.Count("commerce.affected_count", 1),
					audit.Count("commerce.recovery_attempts", 0),
				}
			},
		); err != nil {
			t.Fatalf("bind %s: %v", action, err)
		}
	}
}
