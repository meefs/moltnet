package protocol

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// validateMachineContractShape is deliberately run before Go's typed decode.
// The contract source therefore controls nested unknown-key rejection,
// requiredness, and JSON scalar types; typed decode remains the allocation
// boundary and the existing validators enforce the semantic relationships.
func validateMachineContractShape(raw json.RawMessage, shapeName string) error {
	envelope, err := decodeJSONEnvelope(string(raw))
	if err != nil {
		return err
	}
	fields, err := machineContractEnvelopeFields(shapeName)
	if err != nil {
		return err
	}
	for key := range envelope {
		if _, ok := fields[key]; !ok {
			return fmt.Errorf("unknown %s field %q", shapeName, key)
		}
	}
	for name, field := range fields {
		value, present := envelope[name]
		if !present {
			if field.Required {
				return fmt.Errorf("missing %s.%s", shapeName, name)
			}
			continue
		}
		if isNull(value) {
			if field.Nullable {
				continue
			}
			return fmt.Errorf("invalid %s.%s", shapeName, name)
		}
		if validateMachineContractField(value, field) != nil {
			return fmt.Errorf("invalid %s.%s", shapeName, name)
		}
	}
	shape, _ := machineContractShape(shapeName)
	return validateMachineContractRelations(envelope, shape.Relations)
}

func validateMachineContractArray(raw json.RawMessage, field MachineContractField) error {
	var values []json.RawMessage
	if err := decodeStrictJSONValue(raw, &values); err != nil {
		return err
	}
	countLimit, itemLimit := machineContractArrayLimits(field.Limit)
	if countLimit > 0 && len(values) > countLimit {
		return fmt.Errorf("array too long")
	}
	seen := map[string]struct{}{}
	for _, rawValue := range values {
		if field.Type == "read_message_array" {
			if err := validateMachineContractShape(rawValue, "read_message"); err != nil {
				return err
			}
			continue
		}
		if field.Type == "message_part_array" {
			if err := validateMachineContractShape(rawValue, "message_part"); err != nil {
				return err
			}
			continue
		}
		var value string
		if err := decodeStrictJSONValue(rawValue, &value); err != nil {
			return err
		}
		if itemLimit > 0 && len(value) > itemLimit {
			return fmt.Errorf("array item too long")
		}
		if field.Type == "unique_identifier_array" {
			if err := validateMachineIdentifier(value, field.Name, itemLimit); err != nil {
				return err
			}
			if _, duplicate := seen[value]; duplicate {
				return fmt.Errorf("duplicate array item")
			}
			seen[value] = struct{}{}
		}
		if field.Type == "member_id_array" {
			if err := ValidateMemberID(value); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMachineContractInteger(value int, limit string) error {
	if !strings.HasPrefix(limit, "1..") {
		return nil
	}
	if value < 1 || value > machineContractLimit(strings.TrimPrefix(limit, "1..")) {
		return fmt.Errorf("integer out of range")
	}
	return nil
}

func validateMachineContractByteLimit(value, limit string) error {
	if limit == "" || strings.Contains(limit, "..") || strings.Contains(limit, "|") {
		return nil
	}
	if maximum := machineContractLimit(limit); maximum > 0 && len(value) > maximum {
		return fmt.Errorf("string too long")
	}
	return nil
}

func machineContractArrayLimits(limit string) (int, int) {
	parts := strings.Split(limit, "|")
	if len(parts) == 0 {
		return 0, 0
	}
	if len(parts) == 1 {
		return machineContractLimit(parts[0]), 0
	}
	return machineContractLimit(parts[0]), machineContractLimit(parts[1])
}

func machineContractLimit(name string) int { return machineContractLimits()[name] }

func machineContractEnumSliceContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func machineContractPatternMatches(pattern, value string) bool {
	for name, grammar := range machineContractGrammarValues {
		if grammar.Pattern == pattern {
			return machineContractGrammarMatches(name, value)
		}
	}
	matched, err := regexp.MatchString(pattern, value)
	return err == nil && matched
}
