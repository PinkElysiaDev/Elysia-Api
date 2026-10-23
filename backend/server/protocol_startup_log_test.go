package server

import (
	"bytes"
	"net/http"
	"reflect"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/elysia-api/backend/logging"
	"github.com/elysia-api/backend/protocol"
)

func captureProtocolStartupLogs(server *Server) *bytes.Buffer {
	output := new(bytes.Buffer)
	server.logger = logging.New(logging.Options{Level: "debug", Format: "json"}, nil)
	server.logger.SetOutput(output)
	return output
}

func readProtocolStartupLogs(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var event map[string]any
		if err := sonic.ConfigStd.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func TestProtocolStartupLogsDistinguishEvidenceFromFailures(t *testing.T) {
	for _, defect := range []string{"missing-report", "rejected-report", "missing-revision", "invalid-definition", "storage"} {
		t.Run(defect, func(t *testing.T) {
			server, path := newProtocolAdminTestServer(t)
			if err := server.initializeProtocolRuntime(t.Context()); err != nil {
				t.Fatal(err)
			}
			db := recoveryDB(t, path)
			id := protocol.PresetAnthropicID
			switch defect {
			case "missing-report":
				recoveryExec(t, db, `UPDATE protocol_verification_reports SET compiler_version='previous' WHERE protocol_id=?`, id)
			case "rejected-report":
				recoveryExec(t, db, `UPDATE protocol_verification_reports SET report=json_set(report,'$.passed',json('false')) WHERE protocol_id=?`, id)
			case "missing-revision":
				recoveryExec(t, db, `DELETE FROM protocol_revisions WHERE protocol_id=?`, id)
			case "invalid-definition":
				recoveryExec(t, db, `UPDATE protocol_revisions SET definition='{invalid' WHERE protocol_id=?`, id)
			case "storage":
				if err := server.store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			restarted := &Server{store: server.store, config: server.config}
			output := captureProtocolStartupLogs(restarted)
			if _, err := restarted.protocolService(); err != nil {
				t.Fatal("management service must remain available", err)
			}
			events := readProtocolStartupLogs(t, output)
			if len(events) != 1 {
				t.Fatal("expected one initial registry diagnostic", events)
			}
			level, message := "error", "protocol registry requires repair"
			if defect == "missing-report" {
				level, message = "info", "protocol evidence refresh required"
			}
			if events[0]["level"] != level || events[0]["message"] != message || events[0]["compiler"] != protocol.CompilerVersion {
				t.Fatal("incorrect startup classification", events)
			}
		})
	}
}

func TestProtocolStartupEvidenceLogsAndRestartIdempotence(t *testing.T) {
	server, path := newProtocolAdminTestServer(t)
	if err := server.initializeProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	db := recoveryDB(t, path)
	recoveryExec(t, db, `UPDATE protocol_verification_reports SET compiler_version='previous'`)
	restarted := &Server{store: server.store, config: server.config}
	output := captureProtocolStartupLogs(restarted)
	if err := restarted.initializeProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	var refreshed, stages int
	for _, event := range readProtocolStartupLogs(t, output) {
		if event["level"] == "error" {
			t.Fatal("successful evidence upgrade reported a failure", event)
		}
		switch event["message"] {
		case "protocol evidence refreshed":
			refreshed++
			if event["revision"] != false || event["activation"] != false || event["draft"] != false || event["evidence"] != true {
				t.Fatal("evidence refresh claimed a content change", event)
			}
		case "protocol initialization stage completed":
			stages++
			if event["compiler"] != protocol.CompilerVersion || event["protocol_count"] != float64(4) || event["duration_ms"] == nil {
				t.Fatal("missing stage metrics", event)
			}
		case "protocol refresh completed":
			if event["protocol_count"] != float64(4) || event["evidence_count"] != float64(4) || event["definition_count"] != float64(0) || event["duration_ms"] == nil {
				t.Fatal("incorrect refresh counts", event)
			}
		}
	}
	if refreshed != 4 || stages != 2 {
		t.Fatalf("evidence=%d stages=%d", refreshed, stages)
	}
	counts := recoveryCounts(t, db)
	healthy := &Server{store: server.store, config: server.config}
	output = captureProtocolStartupLogs(healthy)
	if err := healthy.initializeProtocolRuntime(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(counts, recoveryCounts(t, db)) {
		t.Fatal("healthy restart rewrote evidence")
	}
	for _, event := range readProtocolStartupLogs(t, output) {
		if event["message"] != "protocol initialization stage completed" {
			t.Fatal("healthy restart reported recovery", event)
		}
	}
}

func TestProtocolActivationMissingReportRemainsBadRequest(t *testing.T) {
	server, path := newProtocolAdminTestServer(t)
	compiled := activateGatewayDefinition(t, server, loadGatewayDefinition(t, "text-alpha"))
	db := recoveryDB(t, path)
	recoveryExec(t, db, `DELETE FROM protocol_verification_reports WHERE protocol_id=?`, "text-alpha")
	server.setupProtocolRevisionRoutes(server.engine.Group("/api/admin"))
	body, _ := sonic.ConfigStd.Marshal(map[string]string{"revisionHash": compiled.Hash(), "expectedActive": compiled.Hash()})
	response := revisionAdminRequest(t, server.engine, http.MethodPost, "/api/admin/protocols/text-alpha/activate", body, "")
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte("verification_required")) {
		t.Fatalf("missing report changed HTTP classification: %d %s", response.Code, response.Body)
	}
	response = revisionAdminRequest(t, server.engine, http.MethodPost, "/api/admin/protocols/text-alpha/activate", []byte(`{"revisionHash":"missing"}`), "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing revision changed HTTP classification: %d %s", response.Code, response.Body)
	}
}
