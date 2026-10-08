package v1alpha1_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsinstall "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/install"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

// loadValidators builds a schema validator per Kind from the generated CRDs, so
// the samples are checked against exactly the schema the API server would apply.
func loadValidators(t *testing.T) map[string]validation.SchemaValidator {
	t.Helper()

	crdFiles, err := filepath.Glob("../../config/crd/bases/*.yaml")
	if err != nil {
		t.Fatalf("glob CRDs: %v", err)
	}
	if len(crdFiles) == 0 {
		t.Fatal("no generated CRDs found; run 'make manifests'")
	}

	// The schema validator takes the internal representation. Convert through the
	// scheme rather than a JSON round trip: the internal JSONSchemaPropsOrArray
	// has no custom unmarshaller, so a round trip silently drops the schema under
	// every "items", which would leave this test blind to any constraint nested
	// inside an array.
	scheme := runtime.NewScheme()
	apiextensionsinstall.Install(scheme)

	validators := map[string]validation.SchemaValidator{}
	for _, path := range crdFiles {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var versioned apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &versioned); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		var crd apiextensions.CustomResourceDefinition
		if err := scheme.Convert(&versioned, &crd, nil); err != nil {
			t.Fatalf("convert %s to the internal type: %v", path, err)
		}

		for _, version := range crd.Spec.Versions {
			// When every served version shares one schema, the conversion hoists
			// it to the top-level Validation field instead of per-version Schema.
			schema := version.Schema
			if schema == nil {
				schema = crd.Spec.Validation
			}
			if schema == nil || schema.OpenAPIV3Schema == nil {
				t.Fatalf("%s version %s has no schema", path, version.Name)
			}
			validator, _, err := validation.NewSchemaValidator(schema.OpenAPIV3Schema)
			if err != nil {
				t.Fatalf("build validator for %s: %v", crd.Spec.Names.Kind, err)
			}
			validators[crd.Spec.Names.Kind+"/"+version.Name] = validator
		}
	}
	return validators
}

// TestValidatorSeesNestedConstraints guards the conversion above. If the
// internal schema ever loses what is under "items" again, every nested
// assertion in this file would pass vacuously, so assert the depth directly.
func TestValidatorSeesNestedConstraints(t *testing.T) {
	validators := loadValidators(t)
	if validators["PrivateDNSZone/v1alpha1"] == nil {
		t.Fatal("expected a zone validator")
	}

	// A record type nested inside spec.records[] must still be enum-checked.
	invalid := map[string]interface{}{"spec": map[string]interface{}{
		"zone": "example.com",
		"records": []interface{}{map[string]interface{}{
			"name": "api", "type": "NAPTR", "values": []interface{}{"x"},
		}},
	}}
	if errs := validation.ValidateCustomResource(nil, invalid, validators["PrivateDNSZone/v1alpha1"]); len(errs) == 0 {
		t.Fatal("constraints nested under an array are not being evaluated, so this file's nested assertions would be vacuous")
	}
}

// TestShippedSamplesMatchTheCRDSchema validates every sample in config/samples
// against the generated CRDs. Samples are the first thing a user copies, and
// nothing else in the build would notice if one drifted away from the schema.
func TestShippedSamplesMatchTheCRDSchema(t *testing.T) {
	validators := loadValidators(t)

	var sampleFiles []string
	err := filepath.WalkDir("../../config/samples", func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".yaml") || entry.Name() == "kustomization.yaml" {
			return nil
		}
		sampleFiles = append(sampleFiles, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk samples: %v", err)
	}
	if len(sampleFiles) == 0 {
		t.Fatal("no samples found")
	}

	checked := 0
	for _, path := range sampleFiles {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, doc := range strings.Split(string(raw), "\n---") {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			var object map[string]interface{}
			if err := yaml.Unmarshal([]byte(doc), &object); err != nil {
				t.Errorf("%s: parse: %v", path, err)
				continue
			}

			kind, _ := object["kind"].(string)
			apiVersion, _ := object["apiVersion"].(string)
			version := apiVersion
			if idx := strings.Index(apiVersion, "/"); idx >= 0 {
				version = apiVersion[idx+1:]
			}

			validator, ok := validators[kind+"/"+version]
			if !ok {
				t.Errorf("%s: no generated CRD covers %s (%s)", path, kind, apiVersion)
				continue
			}
			if errs := validation.ValidateCustomResource(nil, object, validator); len(errs) > 0 {
				t.Errorf("%s: %s %q does not satisfy the CRD schema: %v",
					path, kind, object["metadata"], errs.ToAggregate())
			}
			checked++
		}
	}
	t.Logf("validated %d sample resources against the generated CRDs", checked)
}

// TestCRDSchemaRejectsInvalidResources confirms the generated schema actually
// enforces the markers on the Go types, rather than silently accepting anything.
func TestCRDSchemaRejectsInvalidResources(t *testing.T) {
	validators := loadValidators(t)
	zoneValidator := validators["PrivateDNSZone/v1alpha1"]
	recordValidator := validators["PrivateDNSRecord/v1alpha1"]
	if zoneValidator == nil || recordValidator == nil {
		t.Fatal("expected validators for both kinds")
	}

	tests := []struct {
		name      string
		validator validation.SchemaValidator
		object    map[string]interface{}
	}{
		{
			name:      "zone without a suffix",
			validator: zoneValidator,
			object:    map[string]interface{}{"spec": map[string]interface{}{}},
		},
		{
			name:      "zone with no spec at all",
			validator: zoneValidator,
			object:    map[string]interface{}{},
		},
		{
			name:      "zone with a zero TTL",
			validator: zoneValidator,
			object: map[string]interface{}{"spec": map[string]interface{}{
				"zone": "example.com", "ttl": 0,
			}},
		},
		{
			name:      "zone with an unknown policy",
			validator: zoneValidator,
			object: map[string]interface{}{"spec": map[string]interface{}{
				"zone": "example.com", "unresolvedRecordPolicy": "Maybe",
			}},
		},
		{
			name:      "inline record with an unsupported type",
			validator: zoneValidator,
			object: map[string]interface{}{"spec": map[string]interface{}{
				"zone": "example.com",
				"records": []interface{}{map[string]interface{}{
					"name": "api", "type": "NAPTR", "values": []interface{}{"x"},
				}},
			}},
		},
		{
			name:      "record with no values",
			validator: recordValidator,
			object: map[string]interface{}{"spec": map[string]interface{}{
				"zoneRef": map[string]interface{}{"name": "z"},
				"name":    "api", "type": "A", "values": []interface{}{},
			}},
		},
		{
			name:      "record with an empty zoneRef name",
			validator: recordValidator,
			object: map[string]interface{}{"spec": map[string]interface{}{
				"zoneRef": map[string]interface{}{"name": ""},
				"name":    "api", "type": "A", "values": []interface{}{"10.0.0.1"},
			}},
		},
		{
			name:      "record missing zoneRef",
			validator: recordValidator,
			object: map[string]interface{}{"spec": map[string]interface{}{
				"name": "api", "type": "A", "values": []interface{}{"10.0.0.1"},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if errs := validation.ValidateCustomResource(nil, test.object, test.validator); len(errs) == 0 {
				t.Error("the CRD schema accepted a resource it should reject")
			}
		})
	}
}
