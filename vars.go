package pathwalk

import "bytes"

// ApplyVars substitutes all {{KEY}} placeholders in data with the corresponding
// values from vars. Keys not present in vars are left unchanged.
//
// Typical use: inject secrets, tokens, or runtime config into pathway JSON
// before calling ParsePathwayBytes.
//
//	modified := pathwalk.ApplyVars(rawJSON, map[string]string{
//	    "API_KEY": os.Getenv("MY_API_KEY"),
//	    "BASE_URL": "https://api.example.com",
//	})
//	pathway, err := pathwalk.ParsePathwayBytes(modified)
func ApplyVars(data []byte, vars map[string]string) []byte {
	for k, v := range vars {
		data = bytes.ReplaceAll(data, []byte("{{"+k+"}}"), []byte(v))
	}
	return data
}
