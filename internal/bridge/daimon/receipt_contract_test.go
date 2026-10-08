package daimon

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/noopolis/moltnet/internal/bridge/loop"
)

// Daimon publishes its wake-receipt item schema in its runtime contract
// manifest (@noopolis/daimon dist/runtime/contract-manifest.json,
// activityV2ResponseSchema.properties.items.items). The vendored copy below is
// checked against the published package in CI by
// scripts/verify-daimon-receipt-schema.sh, which also refreshes it with
// --write. A Daimon field or code this decoder refuses fails here instead of
// parking every receipt job in production.
const daimonReceiptSchemaPath = "testdata/daimon.wake-receipt-status.v2.json"

// The schema describes an activity listing item: Daimon's
// OrganizationRuntimeActivityV2Item is the wake-receipt status plus these
// listing-only fields, which GET /v2/wake-receipts/:id never serves.
var activityOnlyReceiptFields = map[string]bool{"active": true, "queue_position": true}

type receiptPropertySchema struct {
	Type    string            `json:"type"`
	Const   json.RawMessage   `json:"const"`
	Enum    []json.RawMessage `json:"enum"`
	Minimum *int              `json:"minimum"`
}

type receiptItemSchema struct {
	AdditionalProperties bool                             `json:"additionalProperties"`
	Required             []string                         `json:"required"`
	Properties           map[string]receiptPropertySchema `json:"properties"`
}

func loadDaimonReceiptSchema(t *testing.T) receiptItemSchema {
	t.Helper()
	contents, err := os.ReadFile(daimonReceiptSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var vendored struct {
		Schema receiptItemSchema `json:"schema"`
	}
	if err := json.Unmarshal(contents, &vendored); err != nil {
		t.Fatal(err)
	}
	schema := vendored.Schema
	if schema.AdditionalProperties || len(schema.Properties) == 0 {
		t.Fatalf("vendored Daimon receipt schema is not a closed object schema")
	}
	if got := string(schema.Properties["version"].Const); got != `"`+wakeReceiptVersion+`"` {
		t.Fatalf("vendored Daimon receipt schema version = %s, want %q", got, wakeReceiptVersion)
	}
	for field := range activityOnlyReceiptFields {
		if _, ok := schema.Properties[field]; !ok {
			t.Fatalf("activity-only field %q is gone from Daimon's schema; drop it here", field)
		}
	}
	return schema
}

func contractAcceptance() loop.ControlAcceptance {
	job := trackerTestJob()
	return loop.ControlAcceptance{ID: job.AcceptanceID, AgentID: job.RuntimeAgentID, DeliveryID: job.DeliveryID, RequestDigest: job.RequestDigest, AcceptedAt: job.AcceptedAt}
}

// contractReceipt is a schema-valid failed receipt: the state that admits both
// code and text, so every optional property can be added to it alone.
func contractReceipt() map[string]any {
	job := trackerTestJob()
	return map[string]any{
		"version": wakeReceiptVersion, "acceptance_id": job.AcceptanceID, "agent_id": job.RuntimeAgentID,
		"delivery_id": job.DeliveryID, "request_digest": job.RequestDigest, "state": "failed",
		"accepted_at": job.AcceptedAt.Format("2006-01-02T15:04:05.000Z"),
		"updated_at":  job.AcceptedAt.Add(time.Minute).Format("2006-01-02T15:04:05.000Z"),
	}
}

func decodeContractReceipt(t *testing.T, payload map[string]any) error {
	t.Helper()
	contents, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	response := daimonResponse(http.StatusOK, string(contents))
	defer response.Body.Close()
	_, err = decodeWakeReceipt(response, contractAcceptance())
	return err
}

func sampleSchemaValue(t *testing.T, name string, property receiptPropertySchema) any {
	t.Helper()
	var value any
	switch {
	case property.Const != nil:
		if err := json.Unmarshal(property.Const, &value); err != nil {
			t.Fatal(err)
		}
	case len(property.Enum) > 0:
		if err := json.Unmarshal(property.Enum[0], &value); err != nil {
			t.Fatal(err)
		}
	case property.Type == "boolean":
		value = true
	case property.Type == "integer":
		value = 1
		if property.Minimum != nil {
			value = *property.Minimum
		}
	case property.Type == "string":
		value = "2d221b2a-6dd2-4b6c-80cb-a9834d2f85cd"
	default:
		t.Fatalf("Daimon receipt property %q has a shape this test cannot sample; extend it", name)
	}
	return value
}

func TestDecodeWakeReceiptAcceptsEveryDaimonSchemaProperty(t *testing.T) {
	schema := loadDaimonReceiptSchema(t)
	base := contractReceipt()
	if err := decodeContractReceipt(t, base); err != nil {
		t.Fatalf("schema-valid base receipt refused: %v", err)
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if activityOnlyReceiptFields[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			payload := contractReceipt()
			if _, required := payload[name]; !required {
				payload[name] = sampleSchemaValue(t, name, schema.Properties[name])
			}
			if err := decodeContractReceipt(t, payload); err != nil {
				t.Fatalf("Daimon schema allows %q but the decoder refuses it: %v", name, err)
			}
		})
	}
}

func TestDecodeWakeReceiptRequiresExactlyDaimonRequiredFields(t *testing.T) {
	schema := loadDaimonReceiptSchema(t)
	required := map[string]bool{}
	for _, name := range schema.Required {
		if !activityOnlyReceiptFields[name] {
			required[name] = true
		}
	}
	for name := range contractReceipt() {
		t.Run(name, func(t *testing.T) {
			if !required[name] {
				t.Fatalf("base receipt carries %q, which Daimon's schema does not require", name)
			}
			payload := contractReceipt()
			delete(payload, name)
			if err := decodeContractReceipt(t, payload); err == nil {
				t.Fatalf("decoder accepted a receipt without required %q", name)
			}
		})
	}
	if len(required) != len(contractReceipt()) {
		t.Fatalf("Daimon requires %v; the base receipt does not carry all of them", schema.Required)
	}
}

func TestDecodeWakeReceiptAcceptsEveryDaimonCodeAndState(t *testing.T) {
	schema := loadDaimonReceiptSchema(t)
	for _, field := range []string{"code", "state"} {
		if len(schema.Properties[field].Enum) == 0 {
			t.Fatalf("Daimon schema has no %s enum", field)
		}
		for _, raw := range schema.Properties[field].Enum {
			var value string
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			t.Run(field+"="+value, func(t *testing.T) {
				payload := contractReceipt()
				payload[field] = value
				if err := decodeContractReceipt(t, payload); err != nil {
					t.Fatalf("Daimon schema allows %s %q but the decoder refuses it: %v", field, value, err)
				}
			})
		}
	}
}

func TestDecodeWakeReceiptRejectsAPropertyOutsideDaimonSchema(t *testing.T) {
	schema := loadDaimonReceiptSchema(t)
	const unknown = "not_in_daimon_schema"
	if _, ok := schema.Properties[unknown]; ok {
		t.Fatalf("Daimon schema now declares %q; pick another unknown name", unknown)
	}
	payload := contractReceipt()
	payload[unknown] = true
	if err := decodeContractReceipt(t, payload); err == nil {
		t.Fatal("decoder accepted a property Daimon's schema does not declare")
	}
}
