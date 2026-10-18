//go:build !windows

package main

import "fmt"

func runUI(*App)                     { select {} }
func singleInstance() bool           { return true }
func setAutostart(bool) error        { return nil }
func showMessage(title, text string) { fmt.Println(title + ": " + text) }
func showError(text string)          { fmt.Println("Помилка: " + text) }
