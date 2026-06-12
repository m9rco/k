package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// writeSSE writes one event in Server-Sent Events wire format. The Seq becomes
// the SSE `id:` so the browser sends it back as Last-Event-ID on reconnect.
func writeSSE(w http.ResponseWriter, ev Event) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, payload)
}

// parseLastEventID parses the Last-Event-ID header into a sequence number.
// Invalid or empty values mean "from the beginning" (0).
func parseLastEventID(v string) int {
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
