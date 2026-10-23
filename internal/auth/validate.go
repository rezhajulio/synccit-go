// SPDX-License-Identifier: GPL-3.0-or-later
// Port of synccit by James Wilson / drakeapps (https://github.com/drakeapps/synccit), GPL-3.0-or-later.

package auth

import (
	"regexp"
	"strings"
)

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// ValidateAccount applies the original createAccount() validation rules and
// error strings, in the original order (each failed check replaces the
// previous error, as in the PHP code). It returns "" when valid.
func ValidateAccount(username, password, email string) string {
	errMsg := ""
	if email != "" && len(strings.Split(email, "@")) != 2 {
		errMsg = "email not valid"
	}
	if len(username) < 3 {
		errMsg = "username needs to be at least 3 characters long"
	}
	if len(password) < 6 {
		errMsg = "password needs to be at least 6 characters long"
	}
	if !usernamePattern.MatchString(username) {
		errMsg = "username must consist of letters, numbers, or underscores"
	}
	return errMsg
}

// ValidatePassword applies the original password length rule.
func ValidatePassword(password string) string {
	if len(password) < 6 {
		return "password needs to be at least 6 characters long"
	}
	return ""
}
