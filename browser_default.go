package main

import (
	"encoding/json"
	"strings"
)

const chromeBundleIdentifier = "com.google.Chrome"

type launchServicesHandler struct {
	URLScheme  string `json:"LSHandlerURLScheme"`
	RoleAll    string `json:"LSHandlerRoleAll"`
	RoleViewer string `json:"LSHandlerRoleViewer"`
}

func chromeIsDefaultForScheme(content []byte, scheme string) bool {
	var handlers []launchServicesHandler
	if err := json.Unmarshal(content, &handlers); err != nil {
		return false
	}
	schemes := []string{scheme}
	if strings.EqualFold(scheme, "http") {
		schemes = append(schemes, "https")
	}
	for _, candidate := range schemes {
		for index := len(handlers) - 1; index >= 0; index-- {
			handler := handlers[index]
			if !strings.EqualFold(handler.URLScheme, candidate) {
				continue
			}
			bundleID := handler.RoleAll
			if bundleID == "" {
				bundleID = handler.RoleViewer
			}
			return strings.EqualFold(bundleID, chromeBundleIdentifier)
		}
	}
	return false
}
