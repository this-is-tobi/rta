package wire

import (
	"github.com/this-is-tobi/rta/pkg/plugin"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

// safeties and fieldTypes are exhaustive mappings written once and read in
// both directions. Listing them as data rather than as two switch statements
// is what makes the drift test possible: a constant added to one side and not
// the other is a missing row, and a missing row is a failing test rather than
// a value that silently decodes to the zero one.
var safeties = []struct {
	go_ plugin.Safety
	pb  rtav1.Safety
}{
	{plugin.Read, rtav1.Safety_SAFETY_READ},
	{plugin.Write, rtav1.Safety_SAFETY_WRITE},
	{plugin.Destructive, rtav1.Safety_SAFETY_DESTRUCTIVE},
}

var fieldTypes = []struct {
	go_ plugin.FieldType
	pb  rtav1.FieldType
}{
	{plugin.String, rtav1.FieldType_FIELD_TYPE_STRING},
	{plugin.Int, rtav1.FieldType_FIELD_TYPE_INT},
	{plugin.Bool, rtav1.FieldType_FIELD_TYPE_BOOL},
	{plugin.Float, rtav1.FieldType_FIELD_TYPE_FLOAT},
	{plugin.StringSlice, rtav1.FieldType_FIELD_TYPE_STRING_SLICE},
	{plugin.Text, rtav1.FieldType_FIELD_TYPE_TEXT},
	{plugin.Path, rtav1.FieldType_FIELD_TYPE_PATH},
	{plugin.Secret, rtav1.FieldType_FIELD_TYPE_SECRET},
	{plugin.SecretSlice, rtav1.FieldType_FIELD_TYPE_SECRET_SLICE},
}

var endpointRoles = []struct {
	go_ plugin.EndpointRole
	pb  rtav1.EndpointRole
}{
	{plugin.EndpointNone, rtav1.EndpointRole_ENDPOINT_ROLE_UNSPECIFIED},
	{plugin.EndpointHost, rtav1.EndpointRole_ENDPOINT_ROLE_HOST},
	{plugin.EndpointPort, rtav1.EndpointRole_ENDPOINT_ROLE_PORT},
	{plugin.EndpointAddress, rtav1.EndpointRole_ENDPOINT_ROLE_ADDRESS},
	{plugin.EndpointURL, rtav1.EndpointRole_ENDPOINT_ROLE_URL},
	{plugin.EndpointTLS, rtav1.EndpointRole_ENDPOINT_ROLE_TLS},
}

// EndpointRoleToProto encodes which part of a tunnel an input takes.
func EndpointRoleToProto(r plugin.EndpointRole) rtav1.EndpointRole {
	for _, m := range endpointRoles {
		if m.go_ == r {
			return m.pb
		}
	}
	return rtav1.EndpointRole_ENDPOINT_ROLE_UNSPECIFIED
}

// EndpointRoleFromProto decodes it, and an unrecognised role decodes to
// EndpointNone.
//
// **Unspecified is the safe default here, unlike FieldType's.** A type this
// host does not know had to be reported, because the branch meaning "string"
// is the default one and a plugin's secret would quietly become a string flag.
// A *role* this host does not know means only that the host will not fill that
// input from a tunnel — the input keeps its declared default and the call goes
// where it would have gone with no tunnel at all. Failing to fill is visible
// (the call reaches the plugin's default rather than the cluster) and failing
// closed here would mean refusing to load a plugin over one input the host
// merely cannot point at a forward.
func EndpointRoleFromProto(r rtav1.EndpointRole) plugin.EndpointRole {
	for _, m := range endpointRoles {
		if m.pb == r {
			return m.go_
		}
	}
	return plugin.EndpointNone
}

var surfaces = []struct {
	go_ plugin.Surface
	pb  rtav1.Surface
}{
	{plugin.SurfaceCLI, rtav1.Surface_SURFACE_CLI},
	{plugin.SurfaceTUI, rtav1.Surface_SURFACE_TUI},
	{plugin.SurfaceMCP, rtav1.Surface_SURFACE_MCP},
	{plugin.SurfaceCompletion, rtav1.Surface_SURFACE_COMPLETION},
}

// SafetyToProto encodes a safety class. An unrecognised one encodes as
// unspecified, which the far side must refuse: guessing Read for something
// nobody could name would be guessing "harmless" about the one field whose
// whole job is to say how much harm is possible.
func SafetyToProto(s plugin.Safety) rtav1.Safety {
	for _, m := range safeties {
		if m.go_ == s {
			return m.pb
		}
	}
	return rtav1.Safety_SAFETY_UNSPECIFIED
}

// SafetyFromProto decodes a safety class, reporting whether it was one this
// host knows. A caller must not substitute a default for false — see
// SafetyToProto.
func SafetyFromProto(s rtav1.Safety) (plugin.Safety, bool) {
	for _, m := range safeties {
		if m.pb == s {
			return m.go_, true
		}
	}
	return "", false
}

// FieldTypeToProto encodes an input's type.
func FieldTypeToProto(t plugin.FieldType) rtav1.FieldType {
	for _, m := range fieldTypes {
		if m.go_ == t {
			return m.pb
		}
	}
	return rtav1.FieldType_FIELD_TYPE_UNSPECIFIED
}

// FieldTypeFromProto decodes an input's type, reporting whether it was one
// this host knows.
//
// The bool is the point. Every surface switches on the type to build a flag,
// a form field and a schema property, and the branch that means "string" is
// the default one — so a type this host has never heard of, from a plugin
// built against a newer contract, would quietly become a string flag holding
// what the plugin declared as a secret. Validate rejects the zero value in Go
// for the same reason; this is that rule at the wire.
func FieldTypeFromProto(t rtav1.FieldType) (plugin.FieldType, bool) {
	for _, m := range fieldTypes {
		if m.pb == t {
			return m.go_, true
		}
	}
	return "", false
}

// SurfaceToProto encodes the renderer a request arrived through.
func SurfaceToProto(s plugin.Surface) rtav1.Surface {
	for _, m := range surfaces {
		if m.go_ == s {
			return m.pb
		}
	}
	return rtav1.Surface_SURFACE_UNSPECIFIED
}

// SurfaceFromProto decodes the calling surface.
//
// An unknown surface decodes to SurfaceUnknown rather than to a guess, and
// that is the safe direction: the only legitimate use of this field is trust,
// and every check reads "is this MCP" rather than "is this a human", so a
// surface nobody recognises fails closed on the checks that matter.
func SurfaceFromProto(s rtav1.Surface) plugin.Surface {
	for _, m := range surfaces {
		if m.pb == s {
			return m.go_
		}
	}
	return plugin.SurfaceUnknown
}

// FieldToProto encodes one input declaration.
func FieldToProto(f plugin.Field) *rtav1.Field {
	return &rtav1.Field{
		Name:        f.Name,
		Type:        FieldTypeToProto(f.Type),
		Help:        f.Help,
		Default:     ValueToProto(f.Default),
		Required:    f.Required,
		Positional:  f.Positional,
		Local:       f.Local,
		EnvFallback: f.EnvFallback,
		Endpoint:    EndpointRoleToProto(f.Endpoint),
		Config:      f.Config,
		Options:     f.Options,
		Min:         ValueToProto(f.Min),
		Max:         ValueToProto(f.Max),
		// Suggest is a function, so what crosses is that there is one. The
		// values are fetched on demand: they are information in their own
		// right — the names of somebody's secrets are worth something without
		// the values — and shipping them with the declaration would send them
		// to every caller including the ones that must never see them.
		HasSuggest:  f.Suggest != nil,
		Live:        f.Live,
		TlsAdjacent: f.TLSAdjacent,
	}
}

// FieldFromProto decodes one input declaration.
//
// It reports the unknown-type case rather than swallowing it, and does not
// attach Suggest: the host wraps HasSuggest in a function that makes the RPC,
// because only the host has the connection.
func FieldFromProto(f *rtav1.Field) (plugin.Field, bool) {
	t, ok := FieldTypeFromProto(f.GetType())
	return plugin.Field{
		Name:        f.GetName(),
		Type:        t,
		Help:        f.GetHelp(),
		Default:     ValueFromProto(f.GetDefault()),
		Required:    f.GetRequired(),
		Positional:  f.GetPositional(),
		Local:       f.GetLocal(),
		EnvFallback: f.GetEnvFallback(),
		Endpoint:    EndpointRoleFromProto(f.GetEndpoint()),
		Config:      f.GetConfig(),
		Options:     f.GetOptions(),
		Min:         ValueFromProto(f.GetMin()),
		Max:         ValueFromProto(f.GetMax()),
		Live:        f.GetLive(),
		TLSAdjacent: f.GetTlsAdjacent(),
	}, ok
}

// CapabilityToProto encodes one capability declaration.
//
// Run, Prefill and Suggest do not cross — they are what the service's methods
// are for. What crosses is whether the optional two exist, so a host knows
// whether calling them would mean anything.
func CapabilityToProto(c plugin.Capability) *rtav1.Capability {
	return &rtav1.Capability{
		Id:           c.ID,
		Summary:      c.Summary,
		Description:  c.Description,
		Safety:       SafetyToProto(c.Safety),
		Idempotent:   c.Idempotent,
		Inputs:       mapSlice(c.Inputs, FieldToProto),
		MinWidth:     int32(c.MinWidth),
		Detailed:     c.Detailed,
		NoPreview:    c.NoPreview,
		NeedsGrant:   c.NeedsGrant,
		Scope:        c.Scope,
		HasPrefill:   c.Prefill != nil,
		HostSpecific: c.HostSpecific,
		HumanOnly:    c.HumanOnly,
	}
}

// CapabilityFromProto decodes one capability declaration, without handlers.
//
// The returned Capability has a nil Run and will not pass Validate as it
// stands. That is deliberate and is the host's job: only the host has the
// connection the handler has to call over, so it attaches Run — and Prefill
// and Suggest where the declaration says they exist — and validates after.
// Returning something already runnable would mean this package holding a gRPC
// client, which is the wrong thing for a package a plugin author also links.
//
// unknown lists what this host could not interpret, by field name, with the
// capability's own safety under the empty string. It is not an error here
// because deciding what to do about it is a policy question — refuse the
// capability, refuse the whole plugin, warn — and policy belongs to the host.
// It is not silence either, which is the option that ends with a secret in a
// string flag.
func CapabilityFromProto(c *rtav1.Capability) (plugin.Capability, []string) {
	var unknown []string
	safety, ok := SafetyFromProto(c.GetSafety())
	if !ok {
		unknown = append(unknown, "")
	}
	inputs := mapSlice(c.GetInputs(), func(f *rtav1.Field) plugin.Field {
		field, ok := FieldFromProto(f)
		if !ok {
			unknown = append(unknown, f.GetName())
		}
		return field
	})
	return plugin.Capability{
		ID:           c.GetId(),
		Summary:      c.GetSummary(),
		Description:  c.GetDescription(),
		Safety:       safety,
		Idempotent:   c.GetIdempotent(),
		Inputs:       inputs,
		MinWidth:     int(c.GetMinWidth()),
		Detailed:     c.GetDetailed(),
		NoPreview:    c.GetNoPreview(),
		NeedsGrant:   c.GetNeedsGrant(),
		Scope:        c.GetScope(),
		HostSpecific: c.GetHostSpecific(),
		HumanOnly:    c.GetHumanOnly(),
	}, unknown
}

// PluginToProto encodes a whole declaration.
func PluginToProto(p plugin.Plugin) *rtav1.Plugin {
	return &rtav1.Plugin{
		Name:         p.Name,
		Summary:      p.Summary,
		Version:      p.Version,
		Capabilities: mapSlice(p.Capabilities, CapabilityToProto),
		Needs:        mapSlice(p.Needs, func(n plugin.Need) string { return string(n) }),
	}
}

// PluginFromProto decodes a whole declaration, without handlers. See
// CapabilityFromProto for why, and for what unknown carries.
func PluginFromProto(p *rtav1.Plugin) (plugin.Plugin, map[string][]string) {
	var unknown map[string][]string
	caps := mapSlice(p.GetCapabilities(), func(c *rtav1.Capability) plugin.Capability {
		decoded, u := CapabilityFromProto(c)
		if len(u) > 0 {
			if unknown == nil {
				unknown = map[string][]string{}
			}
			unknown[c.GetId()] = u
		}
		return decoded
	})
	return plugin.Plugin{
		Name:         p.GetName(),
		Summary:      p.GetSummary(),
		Version:      p.GetVersion(),
		Capabilities: caps,
		// Carried across verbatim, unknown members included. Registration is
		// what refuses a need rta does not know, and it must refuse it rather
		// than find it already silently dropped here — a plugin asking for
		// something this build has never heard of is a fact the operator
		// should be told, not one the codec should tidy away.
		Needs: mapSlice(p.GetNeeds(), func(n string) plugin.Need { return plugin.Need(n) }),
	}, unknown
}
