package jobs

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/goravel/framework/contracts/queue"
)

func encode(payload any) ([]queue.Arg, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return []queue.Arg{{Type: "string", Value: string(raw)}}, nil
}

// decode unmarshals the one JSON object a job was given. Field positions in
// args are not read.
func decode(args []any, dest any) error {
	raw, err := json.Marshal(args)
	if err != nil {
		return errors.New("payload is not one json object")
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(raw, &documents); err != nil || len(documents) != 1 {
		return errors.New("payload is not one json object")
	}
	body := documents[0]
	var text string
	if err := json.Unmarshal(body, &text); err == nil {
		body = []byte(text)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("payload is not one json object")
	}
	return nil
}
