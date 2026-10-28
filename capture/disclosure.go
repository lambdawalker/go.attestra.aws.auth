package capture

// Purposes has a bounded language set; country-specific messages are deliberately unsupported.
type Purposes struct {
	EN string `json:"en" pulumi:"en"`
	ES string `json:"es" pulumi:"es"`
	FR string `json:"fr" pulumi:"fr"`
}

func DefaultPurposes() Purposes {
	return Purposes{
		EN: "Scan your ID to provide your identity details. You'll review and confirm the information afterward.",
		ES: "Escanea tu identificación para proporcionar tus datos de identidad. Después podrás revisar y confirmar la información.",
		FR: "Numérisez votre pièce d’identité pour fournir vos renseignements d’identité. Vous pourrez ensuite les vérifier et les confirmer.",
	}
}
func (p Purposes) Language(language string) string {
	if language == "es" && p.ES != "" {
		return "es"
	}
	if language == "fr" && p.FR != "" {
		return "fr"
	}
	return "en"
}
func (p Purposes) Text(language string) string {
	switch p.Language(language) {
	case "es":
		return p.ES
	case "fr":
		return p.FR
	}
	return p.EN
}
func ValidOrigin(v string) bool {
	switch v {
	case "", "CA", "US", "MX", "GT", "BZ":
		return true
	}
	return false
}
