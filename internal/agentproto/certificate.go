package agentproto

import (
	"bytes"
	"errors"
)

type CertificateUpdate struct {
	CertificatePEM string `json:"certificate_pem"`
}

type CertificateUpdateAck struct {
	Fingerprint string `json:"fingerprint"`
}

func DecodeCertificateUpdate(payload []byte) (CertificateUpdate, error) {
	var value CertificateUpdate
	if err := decodeStrictPayload(payload, &value); err != nil {
		return CertificateUpdate{}, err
	}
	if !hasExactUsageFields(payload, "certificate_pem") || len(value.CertificatePEM) == 0 ||
		len(value.CertificatePEM) > 16<<10 || !bytes.HasPrefix([]byte(value.CertificatePEM), []byte("-----BEGIN CERTIFICATE-----")) {
		return CertificateUpdate{}, errors.New("invalid Agent certificate update")
	}
	return value, nil
}

func DecodeCertificateUpdateAck(payload []byte) (CertificateUpdateAck, error) {
	var value CertificateUpdateAck
	if err := decodeStrictPayload(payload, &value); err != nil {
		return CertificateUpdateAck{}, err
	}
	if !hasExactUsageFields(payload, "fingerprint") || !digestPattern.MatchString(value.Fingerprint) {
		return CertificateUpdateAck{}, errors.New("invalid Agent certificate update acknowledgement")
	}
	return value, nil
}
