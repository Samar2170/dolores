package analysis

import (
	"encoding/json"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestPayloadJSONDocument(t *testing.T) {
	raw := bson.RawValue{Type: bson.TypeEmbeddedDocument, Value: []byte{
		0x1a, 0x00, 0x00, 0x00, // document length: 26
		0x02, 'a', 0x00, 0x03, 0x00, 0x00, 0x00, 'x', 'y', 0x00, // string a = "xy"
		0x12, 'n', 0x00, 0x2a, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // int64 n = 42
		0x00,
	}}
	got, err := payloadJSON(raw)
	if err != nil {
		t.Fatalf("payloadJSON: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if m["a"] != "xy" || m["n"] != float64(42) {
		t.Fatalf("unexpected payload: %v", m)
	}
}

func TestPayloadJSONArray(t *testing.T) {
	raw := bson.RawValue{Type: bson.TypeArray, Value: []byte{
		0x0c, 0x00, 0x00, 0x00, // document length: 12
		0x10, '0', 0x00, 0x07, 0x00, 0x00, 0x00, // int32 "0" = 7
		0x00,
	}}
	got, err := payloadJSON(raw)
	if err != nil {
		t.Fatalf("payloadJSON: %v", err)
	}
	var rows []any
	if err := json.Unmarshal(got, &rows); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if len(rows) != 1 || rows[0] != float64(7) {
		t.Fatalf("unexpected payload: %v", rows)
	}
}

func TestPayloadJSONUnsupportedType(t *testing.T) {
	if _, err := payloadJSON(bson.RawValue{Type: bson.TypeString, Value: []byte{0x02, 0x00, 0x00, 0x00, 'x', 0x00}}); err == nil {
		t.Fatal("expected error for non document/array payload")
	}
}

func TestPayloadJSONRoundTrip(t *testing.T) {
	// A realistic tickertape section, stored the way market.Repo does it.
	src := []byte(`[{"displayPeriod":"FY25","incTrev":123.4}]`)
	var payload any
	if err := bson.UnmarshalExtJSON(src, false, &payload); err != nil {
		t.Fatalf("extjson decode: %v", err)
	}
	elem, err := bson.Marshal(bson.M{"payload": payload})
	if err != nil {
		t.Fatalf("marshal doc: %v", err)
	}
	var stored struct {
		Payload bson.RawValue `bson:"payload"`
	}
	if err := bson.Unmarshal(elem, &stored); err != nil {
		t.Fatalf("unmarshal doc: %v", err)
	}

	got, err := payloadJSON(stored.Payload)
	if err != nil {
		t.Fatalf("payloadJSON: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(got, &rows); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if len(rows) != 1 || rows[0]["incTrev"] != 123.4 {
		t.Fatalf("unexpected payload: %v", rows)
	}
}