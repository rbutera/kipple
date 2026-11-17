// Package plantest validates kipple documents against their generated JSON
// Schemas. Only tests import it, so the validator never ships in a binary.
package plantest

import (
	"bytes"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/rbutera/kipple/internal/plan"
)

// Validate checks doc against `kipple schema <name>` (formats asserted).
func Validate(name string, doc []byte) error {
	return ValidateWith(nil, name, doc)
}

// ValidateWith validates doc against schema, or against plan.Schema(name)
// when schema is nil.
func ValidateWith(schema []byte, name string, doc []byte) error {
	if schema == nil {
		var err error
		if schema, err = plan.Schema(name); err != nil {
			return err
		}
	}
	sdoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return fmt.Errorf("schema %s: %w", name, err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	url := "https://github.com/rbutera/kipple/schema/v1/" + name + ".json"
	if err := c.AddResource(url, sdoc); err != nil {
		return err
	}
	sch, err := c.Compile(url)
	if err != nil {
		return fmt.Errorf("compile %s: %w", name, err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return fmt.Errorf("document: %w", err)
	}
	return sch.Validate(inst)
}
