package workflow

import (
	"github.com/lambdawalker/go.attestra.aws.auth/assetlinks"
	"strings"
	"testing"
)

func TestRemoveAssociationPreservesOthers(t *testing.T) {
	a := strings.Repeat("AA:", 31) + "AA"
	b := strings.Repeat("BB:", 31) + "BB"
	apps := []assetlinks.Application{{PackageName: "com.example.app", Fingerprints: []string{a, b}}, {PackageName: "com.other.app", Fingerprints: []string{a}}}
	result := removeAssociation(apps, "com.example.app", strings.ToLower(a))
	if len(result) != 2 || len(result[0].Fingerprints) != 1 || result[0].Fingerprints[0] != b || len(apps[0].Fingerprints) != 2 {
		t.Fatal(result)
	}
	result = removeAssociation(result, "com.example.app", b)
	if len(result) != 1 || result[0].PackageName != "com.other.app" {
		t.Fatal(result)
	}
	result = removeAssociation(result, "com.other.app", a)
	if result == nil || len(result) != 0 {
		t.Fatal("must retain empty approval array")
	}
}
func TestAssociationPublishTargetsOnlyObjectAndRetainsConfigOnFailure(t *testing.T) {
	o := testOptions()
	o.Root = t.TempDir()
	o.Stack = "qa"
	f := &bootstrapFake{fail: "pulumi up"}
	if publishAssociations(f, o, nil) == nil {
		t.Fatal("failure not reported")
	}
	if len(f.calls) != 2 {
		t.Fatal(f.calls)
	}
	if !strings.Contains(f.calls[0], "androidAssociations -- []") {
		t.Fatal(f.calls)
	}
	expected := "pulumi up --stack qa --non-interactive --yes --skip-preview --target urn:pulumi:qa::attestra-auth-email::aws:s3/bucketObjectv2:BucketObjectv2::android-assetlinks"
	if f.calls[1] != expected {
		t.Fatal(f.calls[1])
	}
}
