package core

import (
	"encoding/json"

	"resolvarr/internal/core/engine"
)

// FilterSet holds per-Arr-type filter configurations. Radarr and Sonarr
// get their own FilterConfig because movies and TV often need different
// rules — requiring TrueHD Atmos on TV would reject virtually every
// release, and the "MA WEB-DL" provenance check is meaningless for
// series that don't ship on Movies Anywhere.
//
// Primary + Secondary of the same type share one FilterConfig. The
// runner iterates primary's library first; secondary is a mirror
// target, not an independent scanner.
type FilterSet struct {
	Radarr engine.FilterConfig `json:"radarr"`
	Sonarr engine.FilterConfig `json:"sonarr"`
}

// UnmarshalJSON accepts two JSON shapes so configs written before the
// per-type split continue to load cleanly:
//
//  1. New shape — {"radarr": {...}, "sonarr": {...}}
//  2. Legacy shape — flat FilterConfig {"Quality": true, ...} written
//     before the split. Legacy values land in Radarr; Sonarr stays
//     zero-valued and Load() fills it with DefaultFilterConfig.
//
// Detection is probe-based: a legacy config never has "radarr" or
// "sonarr" at the top level of the filters block.
func (f *FilterSet) UnmarshalJSON(data []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	_, hasR := probe["radarr"]
	_, hasS := probe["sonarr"]
	if hasR || hasS {
		// Use a type alias to prevent UnmarshalJSON from recursing.
		type newShape FilterSet
		var n newShape
		if err := json.Unmarshal(data, &n); err != nil {
			return err
		}
		*f = FilterSet(n)
		return nil
	}
	// Legacy flat — unmarshal as engine.FilterConfig, assign to Radarr.
	// Sonarr stays zero-valued; Load() will fill it.
	var flat engine.FilterConfig
	if err := json.Unmarshal(data, &flat); err != nil {
		return err
	}
	f.Radarr = flat
	return nil
}
