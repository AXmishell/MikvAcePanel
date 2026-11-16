package nodes

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
)

func ParseECHConfigPEM(v string) (string, error) {
	rest := []byte(v)
	var list []byte
	haveKeys := false
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch block.Type {
		case "ECH KEYS":
			haveKeys = len(block.Bytes) > 0
		case "ECH CONFIGS":
			if err := validateECHConfigList(block.Bytes); err != nil {
				return "", err
			}
			list = block.Bytes
		}
	}
	if !haveKeys || len(list) == 0 {
		return "", fmt.Errorf("ECH PEM must contain both ECH KEYS and ECH CONFIGS blocks")
	}
	return base64.StdEncoding.EncodeToString(list), nil
}

func validateECHConfigList(list []byte) error {
	if len(list) < 2 {
		return fmt.Errorf("malformed ECH config list")
	}
	n := int(binary.BigEndian.Uint16(list[:2]))
	body := list[2:]
	if n != len(body) {
		return fmt.Errorf("malformed ECH config list length")
	}
	count := 0
	for len(body) > 0 {
		if len(body) < 4 {
			return fmt.Errorf("malformed ECH config list")
		}
		l := int(binary.BigEndian.Uint16(body[2:4]))
		if len(body) < 4+l {
			return fmt.Errorf("malformed ECH config list")
		}
		body = body[4+l:]
		count++
	}
	if count == 0 {
		return fmt.Errorf("malformed ECH config list")
	}
	return nil
}
