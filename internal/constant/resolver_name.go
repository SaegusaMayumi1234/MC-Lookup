package constant

const (
	ResolverNameMojang   = "mojang"
	ResolverNamePlayerDB = "playerdb"
	ResolverNameAshcon   = "ashcon"
	ResolverNameMowojang = "mowojang"
)

var knownResolverNames = []string{
	ResolverNameMojang,
	ResolverNamePlayerDB,
	ResolverNameAshcon,
	ResolverNameMowojang,
}

func KnownResolverNames() []string {
	return append([]string(nil), knownResolverNames...)
}