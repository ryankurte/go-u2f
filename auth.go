// Go FIDO U2F Library
// Copyright 2015 The Go FIDO U2F Library Authors. All rights reserved.
// Use of this source code is governed by the MIT
// license that can be found in the LICENSE file.

package u2f

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/asn1"
	"math/big"
	"time"
)

// SignRequest creates a request to initiate authentication.
func (c *Challenge) SignRequest() *SignRequestMessage {
	var m SignRequestMessage

	// Build public message fields
	m.AppID = c.AppID
	m.Challenge = encodeBase64(c.Challenge)

	// Add existing keys to request message
	for _, r := range c.RegisteredKeys {
		key := registeredKey{
			Version:   u2fVersion,
			KeyHandle: r.KeyHandle}
		m.RegisteredKeys = append(m.RegisteredKeys, key)
	}

	return &m
}

// Authenticate validates a SignResponse authentication response against an particular Challenge.
// An error is returned if any part of the response fails to validate.
// The latest counter value is returned, which the caller should store.
func (c *Challenge) Authenticate(resp SignResponse) (*Registration, error) {
	if time.Now().Sub(c.Timestamp) > timeout {
		return nil, ErrChallengeExpired
	}

	// Find appropriate registration
	var reg *Registration = nil
	for i := range c.RegisteredKeys {
		var tmp = &c.RegisteredKeys[i]
		if resp.KeyHandle == tmp.KeyHandle {
			reg = tmp
			break
		}
	}

	if reg == nil {
		return nil, ErrWrongKeyHandle
	}

	sigData, err := decodeBase64(resp.SignatureData)
	if err != nil {
		return nil, err
	}

	clientData, err := decodeBase64(resp.ClientData)
	if err != nil {
		return nil, err
	}

	ar, err := parseSignResponse(sigData)
	if err != nil {
		return nil, err
	}

	if ar.Counter < reg.Counter {
		return nil, ErrCounterLow
	}
	reg.Counter = ar.Counter

	if err := verifyClientData(clientData, *c); err != nil {
		return nil, err
	}

	// Unmarshal registration public key
	publicKeyDecoded, err := decodeBase64(reg.PublicKey)
	var PublicKey ecdsa.PublicKey
	PublicKey.X, PublicKey.Y = elliptic.Unmarshal(elliptic.P256(), publicKeyDecoded)
	PublicKey.Curve = elliptic.P256()

	if err := verifyAuthSignature(*ar, &PublicKey, c.AppID, clientData); err != nil {
		return nil, err
	}

	if !ar.UserPresenceVerified {
		return nil, ErrUserNotPresent
	}

	return reg, nil
}

type ecdsaSig struct {
	R, S *big.Int
}

type authResp struct {
	UserPresenceVerified bool
	Counter              uint32
	sig                  ecdsaSig
	raw                  []byte
}

func parseSignResponse(sd []byte) (*authResp, error) {
	if len(sd) < 5 {
		return nil, ErrDataShort
	}

	var ar authResp

	userPresence := sd[0]
	if userPresence|1 != 1 {
		return nil, ErrInvalidPresense
	}
	ar.UserPresenceVerified = userPresence == 1

	ar.Counter = uint32(sd[1])<<24 | uint32(sd[2])<<16 | uint32(sd[3])<<8 | uint32(sd[4])

	ar.raw = sd[:5]

	rest, err := asn1.Unmarshal(sd[5:], &ar.sig)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, ErrTrailingData
	}

	return &ar, nil
}

func verifyAuthSignature(ar authResp, pubKey *ecdsa.PublicKey, appID string, clientData []byte) error {
	appParam := sha256.Sum256([]byte(appID))
	challenge := sha256.Sum256(clientData)

	var buf []byte
	buf = append(buf, appParam[:]...)
	buf = append(buf, ar.raw...)
	buf = append(buf, challenge[:]...)
	hash := sha256.Sum256(buf)

	if !ecdsa.Verify(pubKey, hash[:], ar.sig.R, ar.sig.S) {
		return ErrInvalidSig
	}

	return nil
}
