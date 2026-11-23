package provider

import (
	"context"
	"encoding/json"
	"errors"
)

// ReadCodexAccountID observes the login that Codex currently uses without
// refreshing, writing or returning any of its credentials.
func ReadCodexAccountID() (string, error) {
	if _, err := readCodexStoreSetting(codexHome()); err != nil {
		return "", err
	}
	raw, _, err := readCodexLoginFromStore(codexHome(), false)
	if err != nil {
		return "", err
	}
	var login codexAuth
	if json.Unmarshal(raw, &login) != nil || (login.AuthMode != "" && login.AuthMode != "chatgpt" && login.AuthMode != "chatgptAuthTokens") {
		return "", errors.New("Codex login could not be read")
	}
	id := codexAccountID(login)
	if id == "" || login.Tokens.AccessToken == "" {
		return "", errors.New("Codex account is unavailable")
	}
	return id, nil
}

// ReadCodexAllowance explicitly reads the existing Codex login for one
// quota request. It never discovers other accounts, refreshes credentials,
// or writes the sign-in. The login remains owned by Codex.
func ReadCodexAllowance(ctx context.Context) (account, plan string, windows []QuotaWindow, err error) {
	raw, _, err := readCodexLoginFromStore(codexHome(), false)
	if err != nil {
		return "", "", nil, err
	}
	token, account, err := storedCodexAccess(raw)
	if err != nil {
		return "", "", nil, err
	}
	plan, windows, _, _, _, err = codexWindows(ctx, token, account)
	return account, plan, windows, err
}
