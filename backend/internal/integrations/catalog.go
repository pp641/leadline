package integrations

// The integration catalog: every provider a workspace can connect, the
// credentials it needs (rendered as a form by the UI), what it can do, and —
// for lead sources — the search form used by Leads → Import.

// Field is one input in a connect or search form.
type Field struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // secret | text | url | email | number | select | toggle | textarea | list
	Required    bool     `json:"required,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Help        string   `json:"help,omitempty"`
	Default     string   `json:"default,omitempty"`
	Options     []Option `json:"options,omitempty"`
}

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Capabilities a provider can have.
const (
	CapImport = "import"     // Leads → Import searches it
	CapCRM    = "crm_sync"   // pushes leads to a CRM
	CapEmail  = "email_send" // sends campaign email
	CapAPI    = "inbound"    // pushes leads into Leadline through the API
)

// Categories, in display order.
const (
	CatLeadData = "Lead data"
	CatEmail    = "Email finding & verification"
	CatCRM      = "CRM"
	CatSending  = "Email sending"
	CatWorkflow = "Workflows"
)

var Categories = []string{CatLeadData, CatEmail, CatCRM, CatSending, CatWorkflow}

type Provider struct {
	Kind         string   `json:"kind"`
	Name         string   `json:"name"`
	Category     string   `json:"category"`
	Tagline      string   `json:"tagline"`
	Description  string   `json:"description"`
	Website      string   `json:"website"`
	DocsURL      string   `json:"docs_url,omitempty"`
	Color        string   `json:"color"` // brand-ish accent for the UI tile
	Capabilities []string `json:"capabilities"`
	Fields       []Field  `json:"fields"`
	// SearchFields is the Leads → Import form, for CapImport providers.
	SearchFields []Field `json:"search_fields,omitempty"`
	// Notes are shown in the connect dialog (setup steps, cost warnings).
	Notes []string `json:"notes,omitempty"`
}

const (
	KindApollo       = "apollo"
	KindPDL          = "peopledatalabs"
	KindHunter       = "hunter"
	KindCrunchbase   = "crunchbase"
	KindLusha        = "lusha"
	KindSnov         = "snov"
	KindPlaces       = "google_places"
	KindClay         = "clay"
	KindSalesforce   = "salesforce"
	KindPipedrive    = "pipedrive"
	KindZoho         = "zoho_crm"
	KindSES          = "amazon_ses"
	KindSendGrid     = "sendgrid"
	KindMailgun      = "mailgun"
	KindPostmark     = "postmark"
	KindSMTP         = "smtp"
	KindMicrosoft365 = "microsoft_365"
	KindGmail        = "gmail"
)

var sizes = []Option{
	{"", "Any size"}, {"1,10", "1–10"}, {"11,50", "11–50"}, {"51,200", "51–200"},
	{"201,500", "201–500"}, {"501,1000", "501–1,000"}, {"1001,5000", "1,001–5,000"}, {"5001,1000000", "5,000+"},
}

var limitOpts = []Option{{"25", "25"}, {"50", "50"}, {"100", "100"}}

func limitField(def string) Field {
	return Field{Key: "limit", Label: "Max results", Type: "select", Default: def, Options: limitOpts}
}

var Catalog = []Provider{
	// ── Lead data ──
	{
		Kind: KindApollo, Name: "Apollo", Category: CatLeadData, Color: "#5A5CF0",
		Tagline: "B2B people and company search",
		Description: "Search Apollo's B2B database for companies or for people by title, location and company size. " +
			"Optionally reveal work emails (uses Apollo credits).",
		Website: "https://www.apollo.io", DocsURL: "https://docs.apollo.io/reference/people-api-search",
		Capabilities: []string{CapImport},
		Fields: []Field{{Key: "api_key", Label: "API key", Type: "secret", Required: true, Placeholder: "Apollo master API key",
			Help: "Apollo → Settings → Integrations → API → Create new key. People search needs a master key."}},
		SearchFields: []Field{
			{Key: "mode", Label: "Search for", Type: "select", Default: "companies", Options: []Option{{"companies", "Companies"}, {"people", "People (decision makers)"}}},
			{Key: "keywords", Label: "Keywords / industry", Type: "text", Placeholder: "e.g. saas, restaurant, real estate"},
			{Key: "titles", Label: "Job titles (people)", Type: "list", Placeholder: "e.g. Founder, CEO, Marketing Manager"},
			{Key: "locations", Label: "Company locations", Type: "list", Placeholder: "e.g. Noida, India"},
			{Key: "size", Label: "Company size", Type: "select", Options: sizes},
			{Key: "reveal_emails", Label: "Reveal work emails (uses Apollo credits)", Type: "toggle"},
			limitField("25"),
		},
	},
	{
		Kind: KindPDL, Name: "People Data Labs", Category: CatLeadData, Color: "#7C3AED",
		Tagline:     "70M+ company and person profiles",
		Description: "Search People Data Labs' company and person datasets by industry, location and size.",
		Website:     "https://www.peopledatalabs.com", DocsURL: "https://docs.peopledatalabs.com/docs/company-search-api",
		Capabilities: []string{CapImport},
		Fields:       []Field{{Key: "api_key", Label: "API key", Type: "secret", Required: true, Help: "dashboard.peopledatalabs.com → API Keys."}},
		SearchFields: []Field{
			{Key: "mode", Label: "Search for", Type: "select", Default: "companies", Options: []Option{{"companies", "Companies"}, {"people", "People"}}},
			{Key: "industry", Label: "Industry", Type: "text", Placeholder: "e.g. restaurants, computer software"},
			{Key: "titles", Label: "Job titles (people)", Type: "list", Placeholder: "e.g. owner, founder"},
			{Key: "country", Label: "Country", Type: "text", Placeholder: "e.g. india"},
			{Key: "region", Label: "State / region", Type: "text", Placeholder: "e.g. uttar pradesh"},
			{Key: "city", Label: "City", Type: "text", Placeholder: "e.g. noida"},
			{Key: "size", Label: "Company size", Type: "select", Options: []Option{{"", "Any size"}, {"1-10", "1–10"}, {"11-50", "11–50"},
				{"51-200", "51–200"}, {"201-500", "201–500"}, {"501-1000", "501–1,000"}, {"1001-5000", "1,001–5,000"}, {"5001-10000", "5,001–10,000"}, {"10001+", "10,000+"}}},
			limitField("25"),
		},
		Notes: []string{"Each returned record uses one PDL credit."},
	},
	{
		Kind: KindPlaces, Name: "Google Maps Platform", Category: CatLeadData, Color: "#1A73E8",
		Tagline:     "Local businesses from Google Maps",
		Description: "Find local businesses (restaurants, salons, clinics, shops…) with name, address, phone, website and category from the Places API (New).",
		Website:     "https://developers.google.com/maps/documentation/places/web-service", DocsURL: "https://developers.google.com/maps/documentation/places/web-service/text-search",
		Capabilities: []string{CapImport},
		Fields: []Field{{Key: "api_key", Label: "API key", Type: "secret", Required: true, Placeholder: "AIza…",
			Help: "Google Cloud console → APIs & Services → Credentials. Enable “Places API (New)” for the project."}},
		SearchFields: []Field{
			{Key: "query", Label: "What", Type: "text", Required: true, Placeholder: "e.g. restaurants, dental clinics, gyms"},
			{Key: "location", Label: "Where", Type: "text", Required: true, Placeholder: "e.g. Sector 62, Noida"},
			{Key: "only_with_website", Label: "Only businesses with a website", Type: "toggle"},
			{Key: "limit", Label: "Max results", Type: "select", Default: "60", Options: []Option{{"20", "20"}, {"40", "40"}, {"60", "60"}}},
		},
		Notes: []string{"Text Search returns at most 60 places per query; search smaller areas to find more."},
	},
	{
		Kind: KindCrunchbase, Name: "Crunchbase", Category: CatLeadData, Color: "#0288D1",
		Tagline:     "Startups, funding and growth",
		Description: "Find companies by location, category and last funding round — e.g. Series A startups in India.",
		Website:     "https://www.crunchbase.com", DocsURL: "https://data.crunchbase.com/docs/using-search-apis",
		Capabilities: []string{CapImport},
		Fields:       []Field{{Key: "api_key", Label: "API user key", Type: "secret", Required: true, Help: "Requires a Crunchbase API (Enterprise or Applications) licence."}},
		SearchFields: []Field{
			{Key: "location", Label: "Location", Type: "text", Placeholder: "e.g. India, Bengaluru"},
			{Key: "category", Label: "Category", Type: "text", Placeholder: "e.g. SaaS, FinTech"},
			{Key: "funding", Label: "Last funding round", Type: "select", Options: []Option{{"", "Any"}, {"pre_seed", "Pre-seed"}, {"seed", "Seed"},
				{"series_a", "Series A"}, {"series_b", "Series B"}, {"series_c", "Series C"}, {"series_unknown", "Venture (unknown series)"}, {"private_equity", "Private equity"}}},
			{Key: "size", Label: "Employees", Type: "select", Options: []Option{{"", "Any size"}, {"c_00001_00010", "1–10"}, {"c_00011_00050", "11–50"},
				{"c_00051_00100", "51–100"}, {"c_00101_00250", "101–250"}, {"c_00251_00500", "251–500"}, {"c_00501_01000", "501–1,000"}, {"c_01001_05000", "1,001–5,000"}}},
			limitField("25"),
		},
	},
	{
		Kind: KindLusha, Name: "Lusha", Category: CatLeadData, Color: "#6D28D9",
		Tagline:     "B2B company and contact data",
		Description: "Enrich company domains with firmographics from Lusha (industry, size, location, LinkedIn).",
		Website:     "https://www.lusha.com", DocsURL: "https://docs.lusha.com",
		Capabilities: []string{CapImport},
		Fields:       []Field{{Key: "api_key", Label: "API key", Type: "secret", Required: true, Help: "Lusha → Settings → API."}},
		SearchFields: []Field{
			{Key: "domains", Label: "Company domains", Type: "textarea", Required: true, Placeholder: "one per line, e.g.\nacme.com\nexample.in"},
		},
		Notes: []string{"Each company lookup uses Lusha credits."},
	},
	// ── Email finding & verification ──
	{
		Kind: KindHunter, Name: "Hunter", Category: CatEmail, Color: "#FF5722",
		Tagline:     "Emails by domain + verification",
		Description: "Find the people and email addresses behind a company domain (with Hunter's confidence and verification status), or discover companies by description.",
		Website:     "https://hunter.io", DocsURL: "https://hunter.io/api-documentation/v2",
		Capabilities: []string{CapImport},
		Fields:       []Field{{Key: "api_key", Label: "API key", Type: "secret", Required: true, Help: "hunter.io → API → API key."}},
		SearchFields: []Field{
			{Key: "mode", Label: "Search", Type: "select", Default: "domains", Options: []Option{{"domains", "Emails for these domains"}, {"discover", "Discover companies"}}},
			{Key: "domains", Label: "Company domains", Type: "textarea", Placeholder: "one per line, e.g.\nacme.com"},
			{Key: "query", Label: "Describe the companies (discover)", Type: "text", Placeholder: "e.g. restaurants in Noida, India"},
			{Key: "limit", Label: "Emails per domain", Type: "select", Default: "10", Options: []Option{{"5", "5"}, {"10", "10"}, {"25", "25"}}},
		},
	},
	{
		Kind: KindSnov, Name: "Snov.io", Category: CatEmail, Color: "#2563EB",
		Tagline:     "Domain search and email finder",
		Description: "Get company details, generic emails and prospects (names and positions) for company domains.",
		Website:     "https://snov.io", DocsURL: "https://snov.io/api",
		Capabilities: []string{CapImport},
		Fields: []Field{
			{Key: "client_id", Label: "Client ID", Type: "text", Required: true},
			{Key: "client_secret", Label: "Client secret", Type: "secret", Required: true, Help: "Snov.io → Account settings → API."},
		},
		SearchFields: []Field{
			{Key: "domains", Label: "Company domains", Type: "textarea", Required: true, Placeholder: "one per line"},
		},
	},
	// ── CRM ──
	{
		Kind: KindHubSpot, Name: "HubSpot", Category: CatCRM, Color: "#FF7A59",
		Tagline:     "Sync leads as companies + contacts",
		Description: "Push leads to HubSpot as companies and contacts. With auto-sync on, interested leads are pushed automatically.",
		Website:     "https://www.hubspot.com", DocsURL: "https://developers.hubspot.com/docs/api/private-apps",
		Capabilities: []string{CapCRM},
		Fields: []Field{
			{Key: "token", Label: "Private app access token", Type: "secret", Required: true, Placeholder: "pat-…",
				Help: "HubSpot → Settings → Integrations → Private apps. Scopes: crm.objects.companies and crm.objects.contacts (read + write)."},
			{Key: "auto_sync", Label: "Automatically push interested leads", Type: "toggle"},
		},
	},
	{
		Kind: KindSalesforce, Name: "Salesforce", Category: CatCRM, Color: "#00A1E0",
		Tagline:     "Sync leads as Accounts + Contacts",
		Description: "Push leads to Salesforce as Accounts and Contacts using a connected app (OAuth client credentials flow).",
		Website:     "https://www.salesforce.com", DocsURL: "https://help.salesforce.com/s/articleView?id=sf.connected_app_client_credentials_setup.htm",
		Capabilities: []string{CapCRM},
		Fields: []Field{
			{Key: "instance_url", Label: "My Domain URL", Type: "url", Required: true, Placeholder: "https://yourcompany.my.salesforce.com"},
			{Key: "client_id", Label: "Consumer key", Type: "text", Required: true},
			{Key: "client_secret", Label: "Consumer secret", Type: "secret", Required: true,
				Help: "Setup → App Manager → New connected app → enable OAuth, “Enable Client Credentials Flow”, and set a Run As user."},
			{Key: "auto_sync", Label: "Automatically push interested leads", Type: "toggle"},
		},
	},
	{
		Kind: KindPipedrive, Name: "Pipedrive", Category: CatCRM, Color: "#1A1A1A",
		Tagline:     "Sync leads as organizations + persons",
		Description: "Push leads to Pipedrive as organizations and people.",
		Website:     "https://www.pipedrive.com", DocsURL: "https://developers.pipedrive.com/docs/api/v1",
		Capabilities: []string{CapCRM},
		Fields: []Field{
			{Key: "api_token", Label: "API token", Type: "secret", Required: true, Help: "Pipedrive → Personal preferences → API."},
			{Key: "auto_sync", Label: "Automatically push interested leads", Type: "toggle"},
		},
	},
	{
		Kind: KindZoho, Name: "Zoho CRM", Category: CatCRM, Color: "#E42527",
		Tagline:     "Sync leads as Accounts + Contacts",
		Description: "Push leads to Zoho CRM as Accounts and Contacts (OAuth refresh token from a Zoho self client).",
		Website:     "https://www.zoho.com/crm/", DocsURL: "https://www.zoho.com/crm/developer/docs/api/v6/",
		Capabilities: []string{CapCRM},
		Fields: []Field{
			{Key: "data_center", Label: "Data center", Type: "select", Required: true, Default: "in", Options: []Option{
				{"com", "US (zoho.com)"}, {"eu", "EU (zoho.eu)"}, {"in", "India (zoho.in)"}, {"com.au", "Australia (zoho.com.au)"}, {"jp", "Japan (zoho.jp)"}, {"ca", "Canada (zohocloud.ca)"}}},
			{Key: "client_id", Label: "Client ID", Type: "text", Required: true},
			{Key: "client_secret", Label: "Client secret", Type: "secret", Required: true},
			{Key: "refresh_token", Label: "Refresh token", Type: "secret", Required: true,
				Help: "api-console.zoho.com → Self Client → generate a code with scope ZohoCRM.modules.ALL, then exchange it for a refresh token."},
			{Key: "auto_sync", Label: "Automatically push interested leads", Type: "toggle"},
		},
	},
	// ── Email sending ──
	smtpPreset(KindSES, "Amazon SES", "#FF9900", "Send campaigns through Amazon SES (SMTP interface).", "https://aws.amazon.com/ses/",
		[]Field{
			{Key: "region", Label: "Region", Type: "select", Required: true, Default: "ap-south-1", Options: []Option{
				{"us-east-1", "US East (N. Virginia)"}, {"us-west-2", "US West (Oregon)"}, {"eu-west-1", "EU (Ireland)"}, {"eu-central-1", "EU (Frankfurt)"},
				{"ap-south-1", "Asia Pacific (Mumbai)"}, {"ap-southeast-1", "Asia Pacific (Singapore)"}, {"ap-southeast-2", "Asia Pacific (Sydney)"}}},
			{Key: "username", Label: "SMTP username", Type: "text", Required: true},
			{Key: "password", Label: "SMTP password", Type: "secret", Required: true, Help: "SES console → SMTP settings → Create SMTP credentials (not your AWS access keys)."},
		}),
	smtpPreset(KindSendGrid, "SendGrid", "#1A82E2", "Send campaigns through Twilio SendGrid.", "https://sendgrid.com",
		[]Field{{Key: "password", Label: "API key", Type: "secret", Required: true, Placeholder: "SG.…", Help: "SendGrid → Settings → API Keys (Mail Send permission)."}}),
	smtpPreset(KindMailgun, "Mailgun", "#F06B66", "Send campaigns through Mailgun.", "https://www.mailgun.com",
		[]Field{
			{Key: "region", Label: "Region", Type: "select", Required: true, Default: "us", Options: []Option{{"us", "US"}, {"eu", "EU"}}},
			{Key: "username", Label: "SMTP login", Type: "email", Required: true, Placeholder: "postmaster@mg.yourdomain.com"},
			{Key: "password", Label: "SMTP password", Type: "secret", Required: true, Help: "Mailgun → Sending → Domain settings → SMTP credentials."},
		}),
	smtpPreset(KindPostmark, "Postmark", "#FFDE00", "Send campaigns through Postmark (use a broadcast message stream for outreach).", "https://postmarkapp.com",
		[]Field{{Key: "password", Label: "Server API token", Type: "secret", Required: true, Help: "Postmark → Server → API Tokens."}}),
	smtpPreset(KindMicrosoft365, "Microsoft 365 / Outlook", "#0078D4", "Send through a Microsoft 365 mailbox (SMTP AUTH with an app password).", "https://learn.microsoft.com/exchange/clients-and-mobile-in-exchange-online/authenticated-client-smtp-submission",
		[]Field{
			{Key: "username", Label: "Mailbox address", Type: "email", Required: true},
			{Key: "password", Label: "App password", Type: "secret", Required: true, Help: "SMTP AUTH must be enabled for the mailbox; use an app password when MFA is on."},
		}),
	smtpPreset(KindGmail, "Gmail / Google Workspace", "#EA4335", "Send through a Gmail or Google Workspace mailbox (SMTP with an app password).", "https://support.google.com/mail/answer/185833",
		[]Field{
			{Key: "username", Label: "Gmail address", Type: "email", Required: true},
			{Key: "password", Label: "App password", Type: "secret", Required: true, Help: "Google Account → Security → 2-Step Verification → App passwords."},
		}),
	smtpPreset(KindSMTP, "Custom SMTP", "#64748B", "Send through any SMTP relay.", "",
		[]Field{
			{Key: "host", Label: "Host", Type: "text", Required: true, Placeholder: "smtp.example.com"},
			{Key: "port", Label: "Port", Type: "number", Required: true, Default: "587"},
			{Key: "tls", Label: "Security", Type: "select", Default: "starttls", Options: []Option{{"starttls", "STARTTLS"}, {"tls", "TLS"}, {"none", "None"}}},
			{Key: "username", Label: "Username", Type: "text"},
			{Key: "password", Label: "Password", Type: "secret"},
		}),
	// ── Workflows ──
	{
		Kind: KindClay, Name: "Clay", Category: CatWorkflow, Color: "#111827",
		Tagline:     "Send enriched rows from Clay tables",
		Description: "Clay orchestrates enrichment across many providers. Add an HTTP API column in your Clay table that POSTs each row to Leadline, and the rows arrive as leads.",
		Website:     "https://www.clay.com", DocsURL: "https://university.clay.com/docs/http-api-integration-overview",
		Capabilities: []string{CapAPI},
		Fields:       []Field{},
	},
}

func smtpPreset(kind, name, color, desc, site string, fields []Field) Provider {
	return Provider{
		Kind: kind, Name: name, Category: CatSending, Color: color, Tagline: "Campaign email delivery",
		Description: desc + " Used by every mailbox that doesn't have its own SMTP server.",
		Website:     site, Capabilities: []string{CapEmail},
		Fields: append(fields,
			Field{Key: "default_sender", Label: "Use for mailboxes without their own SMTP server", Type: "toggle", Default: "true"}),
		Notes: []string{"The From address of each mailbox must be a verified sender/domain with this provider."},
	}
}

// Lookup returns the catalog entry for kind.
func Lookup(kind string) (Provider, bool) {
	for _, p := range Catalog {
		if p.Kind == kind {
			return p, true
		}
	}
	return Provider{}, false
}

// HasCapability reports whether kind supports cap.
func HasCapability(kind, cap string) bool {
	p, ok := Lookup(kind)
	if !ok {
		return false
	}
	for _, c := range p.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}
