package cmd

// cosmoPlatformsEnvValue returns the GOCOSMOPLATFORMS value for a fat-APE
// build. An empty result means "leave the variable unset", which is the
// fork's everything-default: --cosmo-platforms all was asked for.
func cosmoPlatformsEnvValue(platforms []buildPlatform) string {
	return platformList(platforms)
}
