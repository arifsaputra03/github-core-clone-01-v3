// Package service holds the forge's use cases (see service.go). This file
// holds the Relay global ids the GraphQL API serves: opaque, stable
// base64url strings of the form "<Type>:<numeric id>".
package service

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// GlobalID encodes a numeric id as an opaque global id for a GraphQL type.
func GlobalID(kind string, id int64) string {
	raw := kind + ":" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// ParseGlobalID decodes a global id into its type and numeric id.
func ParseGlobalID(id string) (kind string, numeric int64, err error) {
	raw, decErr := base64.RawURLEncoding.DecodeString(id)
	if decErr != nil {
		// Tolerate the padded spelling too.
		raw, decErr = base64.URLEncoding.DecodeString(id)
		if decErr != nil {
			return "", 0, fmt.Errorf("bad global id %q", id)
		}
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", 0, fmt.Errorf("bad global id %q", id)
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("bad global id %q", id)
	}
	return parts[0], n, nil
}
