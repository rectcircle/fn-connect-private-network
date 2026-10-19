package model

// DNSQuery names are NAS-side names after removing the client's isolation suffix.
// An empty name denotes the suffix apex and resolves to the NAS itself.
// Only IPv4 address queries (Type A) are supported.
type DNSQuery struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type DNSAnswer struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Result       string   `json:"result"`
	Addresses    []string `json:"addresses"`
	TTL          uint32   `json:"ttl"`
	Source       string   `json:"source"`
	ResolvedName string   `json:"resolvedName"`
	Revision     string   `json:"revision"`
}

type DNSPlan struct {
	Suffix     string `json:"suffix"`
	Address    string `json:"address"`
	Port       uint16 `json:"port"`
	Generation string `json:"generation"`
}

type DNSStatus struct {
	State     string `json:"state"`
	Suffix    string `json:"suffix,omitempty"`
	LastError *Error `json:"lastError,omitempty"`
}
