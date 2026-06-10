package classify

// Keyword sets embedded from spec §9.1–9.4. All entries are lowercase
// words/phrases matched on word boundaries.

// genericBusinessSignals — spec §9.1.
var genericBusinessSignals = []string{
	"price",
	"quote",
	"quotation",
	"how much",
	"rate",
	"invoice",
	"payment",
	"deposit",
	"receipt",
	"booking",
	"appointment",
	"available",
	"availability",
	"slot",
	"schedule",
	"reschedule",
	"service",
	"package",
	"delivery",
	"order",
	"refund",
	"contract",
	"proposal",
	"deadline",
	"client",
	"customer",
	"project",
}

// freelancerBusinessSignals — spec §9.2.
var freelancerBusinessSignals = []string{
	"logo",
	"website",
	"copywriting",
	"design",
	"editing",
	"consultation",
	"photoshoot",
	"coaching",
	"tuition",
	"repair",
	"cleaning",
	"renovation",
	"lesson",
	"session",
	"campaign",
	"proposal",
	"deck",
	"video",
	"content",
	"brand",
	"landing page",
	"social media",
	"ad campaign",
}

// serviceBusinessSignals — spec §9.3.
var serviceBusinessSignals = []string{
	"trial class",
	"class timing",
	"session",
	"appointment",
	"cleaning slot",
	"repair quote",
	"consultation",
	"booking fee",
	"package price",
	"reschedule",
	"availability",
	"location",
	"address",
	"deposit",
	"balance payment",
}

// personalSignals — spec §9.4.
var personalSignals = []string{
	"mum",
	"dad",
	"family",
	"bro",
	"sis",
	"dinner",
	"lunch",
	"birthday",
	"holiday",
	"joke",
	"meme",
	"football",
	"party",
	"wedding",
	"baby",
	"private emotional content",
	"relationship language",
	"casual friend slang",
}
