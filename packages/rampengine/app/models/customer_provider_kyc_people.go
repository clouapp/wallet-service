package models

import "encoding/json"

// PersonIDs is a business's people at the provider, by local_ref; empty for a
// person's row, a company before its first application, or a value that does
// not decode.
func (k *CustomerProviderKyc) PersonIDs() map[string]string {
	ids := map[string]string{}
	if k.AssociatedPersonIDs == nil {
		return ids
	}
	if err := json.Unmarshal([]byte(*k.AssociatedPersonIDs), &ids); err != nil {
		return map[string]string{}
	}
	return ids
}

// SetPersonIDs keeps ids as the business's people at the provider; nil keeps
// none.
func (k *CustomerProviderKyc) SetPersonIDs(ids map[string]string) {
	if ids == nil {
		k.AssociatedPersonIDs = nil
		return
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		k.AssociatedPersonIDs = nil
		return
	}
	value := string(encoded)
	k.AssociatedPersonIDs = &value
}
