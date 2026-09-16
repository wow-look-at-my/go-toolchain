package cmd

// cosmoPlatformsEnvValue returns the GOCOSMOPLATFORMS value for a fat-APE
// build.
func cosmoPlatformsEnvValue(platforms []buildPlatform) string {
	return platformList(platforms)
}
