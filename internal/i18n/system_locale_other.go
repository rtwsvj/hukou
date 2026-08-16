//go:build !darwin

package i18n

// defaultSystemGUILocale: no portable GUI-language source outside darwin.
func defaultSystemGUILocale() string { return "" }
