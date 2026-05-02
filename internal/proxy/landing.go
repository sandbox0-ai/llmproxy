package proxy

import (
	"io/fs"
	"net/http"
)

func (h *Handler) serveLanding(w http.ResponseWriter, r *http.Request) {
	index, err := fs.ReadFile(embeddedWeb, "web/index.html")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "landing page unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}
