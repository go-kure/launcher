package components_test

import (
	"strings"
	"testing"

	notificationv1beta3 "github.com/fluxcd/notification-controller/api/v1beta3"

	"github.com/go-kure/launcher/pkg/oam"
	"github.com/go-kure/launcher/pkg/oam/builtin/components"
)

// TestFluxProvider_AddressCredential: under a policy that forbids explicit
// secrets, a fluxcd-provider whose `address` is its credential is refused, and
// the refusal names the type and the remedy without the value: every type that
// posts to its address with no token of its own, slack whatever the address
// is, generic and generic-hmac, which post to the address as written, and an
// azureeventhub connection string with its key. A policy that allows
// explicit secrets, one that does not answer the question and none passed
// build each of them, with the address in the object. The same types with the
// address in the Secret secretRef names, an empty address, an azureeventhub
// endpoint and a type whose address is no credential are built under every one
// of them.
func TestFluxProvider_AddressCredential(t *testing.T) {
	const remedy = "the environment policy forbids explicit secrets; omit address, set secretRef, " +
		"and put the URL under the address key of that Secret, which the controller reads in place of address"
	webhook := "https://hooks.example/services/" + esSentinel
	fromSecret := map[string]any{"name": "provider-address"}
	policies := map[string]oam.Policy{
		"forbidding":       esForbidding(),
		"allowing":         esPolicy{stubPolicy: &stubPolicy{}, allow: true},
		"no answer":        &stubPolicy{},
		"no policy passed": nil,
	}
	type row struct {
		props   map[string]any
		refusal string // under the forbidding policy; empty where it builds
	}
	rows := map[string]row{
		// The public endpoint of a slack Provider that takes its token from the
		// Secret is no credential, but the kind does not read the Secret.
		"a slack Provider at its public endpoint": {
			map[string]any{"type": "slack", "address": "https://slack.com/api/chat.postMessage", "secretRef": fromSecret},
			"address: the address of a slack Provider is the credential it posts with, and " + remedy,
		},
		"an azureeventhub connection string": {
			map[string]any{"type": "azureeventhub", "address": "Endpoint=sb://shop.servicebus.windows.net/;SharedAccessKeyName=send;SharedAccessKey=" + esSentinel + ";EntityPath=events"},
			"address: the address of an azureeventhub Provider holds a SharedAccessKey, so it is a connection string with its key, and " + remedy,
		},
		"an azureeventhub endpoint":                {map[string]any{"type": "azureeventhub", "address": "https://shop.servicebus.windows.net/events", "secretRef": fromSecret}, ""},
		"a github repository":                      {map[string]any{"type": "github", "address": "https://github.com/shop/fleet", "secretRef": fromSecret}, ""},
		"a generic Provider with an empty address": {map[string]any{"type": "generic", "address": ""}, ""},
	}
	credentialTypes := []string{
		notificationv1beta3.DiscordProvider, notificationv1beta3.GenericProvider, notificationv1beta3.GenericHMACProvider,
		notificationv1beta3.GoogleChatProvider, notificationv1beta3.LarkProvider, notificationv1beta3.MSTeamsProvider,
		notificationv1beta3.RocketProvider, notificationv1beta3.SlackProvider,
	}
	for _, typ := range credentialTypes {
		rows["a "+typ+" webhook"] = row{
			map[string]any{"type": typ, "address": webhook},
			"address: the address of a " + typ + " Provider is the credential it posts with, and " + remedy,
		}
		rows["a "+typ+" webhook beside a Secret"] = row{
			map[string]any{"type": typ, "address": webhook, "secretRef": fromSecret},
			"address: the address of a " + typ + " Provider is the credential it posts with, and " + remedy,
		}
		// The remedy: the URL under the Secret's address key, and none here.
		rows["a "+typ+" Provider that reads its address from a Secret"] = row{map[string]any{"type": typ, "secretRef": fromSecret}, ""}
	}
	for name, tc := range rows {
		for policyName, policy := range policies {
			t.Run(name+"/"+policyName, func(t *testing.T) {
				objs, err := pvTransform("fluxcd-provider", &components.FluxcdProviderHandler{}, tc.props, policy)
				if tc.refusal != "" && policyName == "forbidding" {
					htWantViolation(t, err, `component "web": `+tc.refusal)
					if strings.Contains(err.Error(), esSentinel) || strings.Contains(err.Error(), "hooks.example") || strings.Contains(err.Error(), "slack.com") {
						t.Errorf("the refusal carries the value: %v", err)
					}
					rcWantClass(t, err, oam.RefusalExplicitSecret)
					return
				}
				if err != nil {
					t.Fatalf("transform: %v", err)
				}
				if len(objs) != 1 {
					t.Fatalf("generated %d objects, want one", len(objs))
				}
				provider, ok := objs[0].(*notificationv1beta3.Provider)
				if !ok {
					t.Fatalf("generated a %T, want the Provider", objs[0])
				}
				if want, _ := tc.props["address"].(string); provider.Spec.Address != want {
					t.Errorf("address = %q, want %q", provider.Spec.Address, want)
				}
			})
		}
	}
}
