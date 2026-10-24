// Package geo normalizes country names. Company data spells countries in
// several ways ("India", "IN", "india"); ICP filters and facets compare them
// by ISO 3166-1 alpha-2 code.
package geo

import (
	"sort"
	"strings"
)

// Country is an ISO 3166-1 country.
type Country struct {
	Code string `json:"code"` // alpha-2, upper case
	Name string `json:"name"` // English short name
}

const isoList = `AD:Andorra|AE:United Arab Emirates|AF:Afghanistan|AG:Antigua and Barbuda|AI:Anguilla|AL:Albania|AM:Armenia|AO:Angola|AQ:Antarctica|AR:Argentina|AS:American Samoa|AT:Austria|AU:Australia|AW:Aruba|AX:Åland Islands|AZ:Azerbaijan|BA:Bosnia and Herzegovina|BB:Barbados|BD:Bangladesh|BE:Belgium|BF:Burkina Faso|BG:Bulgaria|BH:Bahrain|BI:Burundi|BJ:Benin|BL:Saint Barthélemy|BM:Bermuda|BN:Brunei|BO:Bolivia|BQ:Caribbean Netherlands|BR:Brazil|BS:Bahamas|BT:Bhutan|BW:Botswana|BY:Belarus|BZ:Belize|CA:Canada|CC:Cocos (Keeling) Islands|CD:Congo (DRC)|CF:Central African Republic|CG:Congo|CH:Switzerland|CI:Côte d'Ivoire|CK:Cook Islands|CL:Chile|CM:Cameroon|CN:China|CO:Colombia|CR:Costa Rica|CU:Cuba|CV:Cape Verde|CW:Curaçao|CX:Christmas Island|CY:Cyprus|CZ:Czechia|DE:Germany|DJ:Djibouti|DK:Denmark|DM:Dominica|DO:Dominican Republic|DZ:Algeria|EC:Ecuador|EE:Estonia|EG:Egypt|EH:Western Sahara|ER:Eritrea|ES:Spain|ET:Ethiopia|FI:Finland|FJ:Fiji|FK:Falkland Islands|FM:Micronesia|FO:Faroe Islands|FR:France|GA:Gabon|GB:United Kingdom|GD:Grenada|GE:Georgia|GF:French Guiana|GG:Guernsey|GH:Ghana|GI:Gibraltar|GL:Greenland|GM:Gambia|GN:Guinea|GP:Guadeloupe|GQ:Equatorial Guinea|GR:Greece|GT:Guatemala|GU:Guam|GW:Guinea-Bissau|GY:Guyana|HK:Hong Kong|HN:Honduras|HR:Croatia|HT:Haiti|HU:Hungary|ID:Indonesia|IE:Ireland|IL:Israel|IM:Isle of Man|IN:India|IQ:Iraq|IR:Iran|IS:Iceland|IT:Italy|JE:Jersey|JM:Jamaica|JO:Jordan|JP:Japan|KE:Kenya|KG:Kyrgyzstan|KH:Cambodia|KI:Kiribati|KM:Comoros|KN:Saint Kitts and Nevis|KP:North Korea|KR:South Korea|KW:Kuwait|KY:Cayman Islands|KZ:Kazakhstan|LA:Laos|LB:Lebanon|LC:Saint Lucia|LI:Liechtenstein|LK:Sri Lanka|LR:Liberia|LS:Lesotho|LT:Lithuania|LU:Luxembourg|LV:Latvia|LY:Libya|MA:Morocco|MC:Monaco|MD:Moldova|ME:Montenegro|MF:Saint Martin|MG:Madagascar|MH:Marshall Islands|MK:North Macedonia|ML:Mali|MM:Myanmar|MN:Mongolia|MO:Macao|MP:Northern Mariana Islands|MQ:Martinique|MR:Mauritania|MS:Montserrat|MT:Malta|MU:Mauritius|MV:Maldives|MW:Malawi|MX:Mexico|MY:Malaysia|MZ:Mozambique|NA:Namibia|NC:New Caledonia|NE:Niger|NF:Norfolk Island|NG:Nigeria|NI:Nicaragua|NL:Netherlands|NO:Norway|NP:Nepal|NR:Nauru|NU:Niue|NZ:New Zealand|OM:Oman|PA:Panama|PE:Peru|PF:French Polynesia|PG:Papua New Guinea|PH:Philippines|PK:Pakistan|PL:Poland|PM:Saint Pierre and Miquelon|PN:Pitcairn Islands|PR:Puerto Rico|PS:Palestine|PT:Portugal|PW:Palau|PY:Paraguay|QA:Qatar|RE:Réunion|RO:Romania|RS:Serbia|RU:Russia|RW:Rwanda|SA:Saudi Arabia|SB:Solomon Islands|SC:Seychelles|SD:Sudan|SE:Sweden|SG:Singapore|SH:Saint Helena|SI:Slovenia|SK:Slovakia|SL:Sierra Leone|SM:San Marino|SN:Senegal|SO:Somalia|SR:Suriname|SS:South Sudan|ST:São Tomé and Príncipe|SV:El Salvador|SX:Sint Maarten|SY:Syria|SZ:Eswatini|TC:Turks and Caicos Islands|TD:Chad|TG:Togo|TH:Thailand|TJ:Tajikistan|TK:Tokelau|TL:Timor-Leste|TM:Turkmenistan|TN:Tunisia|TO:Tonga|TR:Türkiye|TT:Trinidad and Tobago|TV:Tuvalu|TW:Taiwan|TZ:Tanzania|UA:Ukraine|UG:Uganda|UM:U.S. Outlying Islands|US:United States|UY:Uruguay|UZ:Uzbekistan|VA:Vatican City|VC:Saint Vincent and the Grenadines|VE:Venezuela|VG:British Virgin Islands|VI:U.S. Virgin Islands|VN:Vietnam|VU:Vanuatu|WF:Wallis and Futuna|WS:Samoa|XK:Kosovo|YE:Yemen|YT:Mayotte|ZA:South Africa|ZM:Zambia|ZW:Zimbabwe`

// Common alternative spellings, keyed by lower-case name.
var aliases = map[string]string{
	"usa": "US", "u.s.": "US", "u.s.a.": "US", "united states of america": "US", "america": "US",
	"uk": "GB", "u.k.": "GB", "great britain": "GB", "britain": "GB", "england": "GB", "scotland": "GB", "wales": "GB",
	"uae": "AE", "bharat": "IN", "hindustan": "IN", "turkey": "TR", "czech republic": "CZ",
	"russian federation": "RU", "republic of korea": "KR", "korea": "KR", "viet nam": "VN",
	"ivory coast": "CI", "burma": "MM", "swaziland": "SZ", "macedonia": "MK", "holland": "NL",
	"the netherlands": "NL", "democratic republic of the congo": "CD", "republic of the congo": "CG",
}

var (
	countries []Country
	byCode    = map[string]Country{}
	byName    = map[string]string{}
)

func init() {
	for _, pair := range strings.Split(isoList, "|") {
		code, name, _ := strings.Cut(pair, ":")
		c := Country{Code: code, Name: name}
		countries = append(countries, c)
		byCode[code] = c
		byName[strings.ToLower(name)] = code
	}
	for k, v := range aliases {
		byName[k] = v
	}
	sort.Slice(countries, func(i, j int) bool { return countries[i].Name < countries[j].Name })
}

// Countries returns every country, sorted by name.
func Countries() []Country { return countries }

// Code returns the alpha-2 code for a country name or code, or "" when unknown.
func Code(s string) string {
	s = strings.TrimSpace(s)
	if len(s) == 2 {
		if _, ok := byCode[strings.ToUpper(s)]; ok {
			return strings.ToUpper(s)
		}
	}
	return byName[strings.ToLower(s)]
}

// Name returns the English name for a code, or the input unchanged.
func Name(code string) string {
	if c, ok := byCode[strings.ToUpper(code)]; ok {
		return c.Name
	}
	return code
}

// Variants returns the lower-case spellings a column may hold for each
// country (its code, its name and known aliases), for SQL matching.
func Variants(vals []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, v := range vals {
		add(v)
		code := Code(v)
		if code == "" {
			continue
		}
		add(code)
		add(byCode[code].Name)
		for alias, c := range aliases {
			if c == code {
				add(alias)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Same reports whether two country spellings name the same country.
func Same(a, b string) bool {
	if ca, cb := Code(a), Code(b); ca != "" && cb != "" {
		return ca == cb
	}
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
