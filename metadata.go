package agentcat

// AttachEventMetadata resolves customer-defined tags and properties via the
// given callbacks and attaches them to the event's wire fields. Integration
// API for adapter modules: each adapter constructs the closures from its
// typed Options callbacks. Either callback may be nil (skipped). A panic in a
// callback is swallowed so the event is still published without that
// metadata; tags are validated via ValidateTags and empty property maps are
// dropped.
func AttachEventMetadata(evt *Event, tags func() map[string]string, properties func() map[string]any) {
	if evt == nil {
		return
	}
	if resolved := resolveTags(tags); resolved != nil {
		evt.Tags = &resolved
	}
	if resolved := resolveProperties(properties); resolved != nil {
		evt.Properties = resolved
	}
}

// resolveTags invokes the tags callback (if any) and validates the result.
// A panic in the callback is swallowed so the event is still published
// without tags.
func resolveTags(tags func() map[string]string) (result map[string]string) {
	if tags == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			result = nil
		}
	}()
	return ValidateTags(tags())
}

// resolveProperties invokes the properties callback (if any). A panic in the
// callback is swallowed so the event is still published without properties.
func resolveProperties(properties func() map[string]any) (result map[string]any) {
	if properties == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			result = nil
		}
	}()
	props := properties()
	if len(props) == 0 {
		return nil
	}
	return props
}
