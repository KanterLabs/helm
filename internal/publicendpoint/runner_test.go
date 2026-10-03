package publicendpoint

import (
	"reflect"
	"testing"
	"time"
)

func TestEdgeEventsTrackConnectionsAndLocations(t *testing.T) {
	r := newRunner("cloudflared")
	r.status.State = "starting"
	for index, location := range []string{"ord08", "iad11", "ord02", "iad11"} {
		r.edgeEvent("2026-10-03T13:03:08Z INF Registered tunnel connection connIndex="+string(rune('0'+index))+" connection=8f0c event=0 ip=198.41.192.27 location="+location+" protocol=quic", true)
	}
	status := r.Status()
	if status.State != "connected" || status.Connections != 4 || status.ConnectedAt == "" {
		t.Fatalf("after four registrations: %+v", status)
	}
	if want := []string{"iad11", "ord02", "ord08"}; !reflect.DeepEqual(status.Locations, want) {
		t.Fatalf("locations = %v, want %v", status.Locations, want)
	}
	connectedAt := status.ConnectedAt

	r.edgeEvent(`2026-10-03T13:04:00Z ERR Connection terminated error="timeout: no recent network activity" connIndex=1`, false)
	if status := r.Status(); status.State != "connected" || status.Connections != 3 || status.ConnectedAt != connectedAt {
		t.Fatalf("one lost connection must keep the tunnel connected: %+v", status)
	}
	for _, index := range []string{"0", "2", "3"} {
		r.edgeEvent("2026-10-03T13:04:01Z INF Unregistered tunnel connection connIndex="+index+" event=0 ip=198.41.192.27", false)
	}
	if status := r.Status(); status.State != "restarting" || status.Connections != 0 || status.ConnectedAt != "" || len(status.Locations) != 0 {
		t.Fatalf("losing every connection must report reconnecting: %+v", status)
	}
	r.edgeEvent("2026-10-03T13:04:05Z INF Registered tunnel connection connIndex=0 location=ord08 protocol=quic", true)
	if status := r.Status(); status.State != "connected" || status.Connections != 1 {
		t.Fatalf("re-registration must reconnect: %+v", status)
	}
}

func TestStatusLocationsAreCopied(t *testing.T) {
	r := newRunner("cloudflared")
	r.edgeEvent("INF Registered tunnel connection connIndex=0 location=ord08", true)
	status := r.Status()
	status.Locations[0] = "mutated"
	if got := r.Status().Locations[0]; got != "ord08" {
		t.Fatalf("Status leaked internal slice: %q", got)
	}
}

func TestProbeNoncesAreOneTimeAndExpire(t *testing.T) {
	var probes probeRegistry
	probes.add("a", time.Minute)
	if !probes.take("a") {
		t.Fatal("pending nonce was refused")
	}
	if probes.take("a") {
		t.Fatal("nonce was accepted twice")
	}
	if probes.take("never-issued") {
		t.Fatal("unknown nonce was accepted")
	}
	probes.add("b", -time.Second)
	if probes.take("b") {
		t.Fatal("expired nonce was accepted")
	}
	probes.add("c", time.Minute)
	probes.drop("c")
	if probes.take("c") {
		t.Fatal("dropped nonce was accepted")
	}
}

func TestAnswerProbeRequiresFullNonce(t *testing.T) {
	m := &Manager{}
	m.probes.add("short", time.Minute)
	if m.AnswerProbe("short") {
		t.Fatal("a nonce that is not 32 hex characters must be refused")
	}
	nonce := randomID()
	m.probes.add(nonce, time.Minute)
	if !m.AnswerProbe(nonce) || m.AnswerProbe(nonce) {
		t.Fatal("a pending nonce must be answered exactly once")
	}
	var missing *Manager
	if missing.AnswerProbe(nonce) {
		t.Fatal("a nil manager must refuse probes")
	}
}
