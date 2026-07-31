//go:build !darwin

package main

func defaultBrowserIsChrome(string) bool {
	return false
}
