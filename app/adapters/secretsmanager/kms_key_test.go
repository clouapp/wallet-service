package secretsmanager

import "testing"

func TestKMS_KeyID_NamesOnlyAKMSReference(t *testing.T) {
	cases := map[string]string{
		"arn:aws:kms:us-east-1:430776404482:key/08bad03a-3e1f-4c9b-99f1-68f58f68d0db": "arn:aws:kms:us-east-1:430776404482:key/08bad03a-3e1f-4c9b-99f1-68f58f68d0db",
		"arn:aws:kms:us-east-1:430776404482:alias/waas-dev-master":                    "arn:aws:kms:us-east-1:430776404482:alias/waas-dev-master",
		"alias/waas-dev-master":                "alias/waas-dev-master",
		"08bad03a-3e1f-4c9b-99f1-68f58f68d0db": "08bad03a-3e1f-4c9b-99f1-68f58f68d0db",
		"  alias/waas-dev-master  ":            "alias/waas-dev-master",
		"local:dev-master-key":                 "",
		"":                                     "",
		"   ":                                  "",
	}
	for ref, want := range cases {
		if got := KMSKeyID(ref); got != want {
			t.Errorf("KMSKeyID(%q) = %q, want %q", ref, got, want)
		}
	}
}
