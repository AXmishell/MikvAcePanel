package server

import (
	"errors"
	"net/http"

	"github.com/axmishell/mikvacepanel/internal/store"
)

func writeStoreError(w http.ResponseWriter, err error, entity string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, entity+" not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, entity+" already exists")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func maxInt(v, def int) int {
	if v > def {
		return v
	}
	return def
}

func clampInt(v, def, min, max int) int {
	if v == 0 {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
