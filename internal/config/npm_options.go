package config

// npm's config options as its parser (nopt) reads them. npmShortValues and
// npmLongValues take a value: npx --cache /tmp/c playwright skips the
// path. Any Boolean option takes an explicit npmBooleanValues word, and one
// listed in npmBooleanLiterals as option:word takes that word too:
// npm --color always exec. Generated from npm 11.19.0's @npmcli/config
// definitions.
const (
	npmShortValues = "-c -L -m -C -w"
	npmLongValues  = "--_auth --access --allow-directory --allow-file --allow-git" +
		" --allow-remote --allow-scripts --also --audit-level --auth-type" +
		" --before --browser --ca --cache --cache-max --cache-min --cafile" +
		" --call --cert --cidr --cpu --depth --diff --diff-dst-prefix" +
		" --diff-src-prefix --diff-unified --editor --expect-result-count" +
		" --expires --fetch-retries --fetch-retry-factor" +
		" --fetch-retry-maxtimeout --fetch-retry-mintimeout" +
		" --fetch-timeout --git --globalconfig --heading --https-proxy" +
		" --include --init-author-email --init-author-name" +
		" --init-author-url --init-license --init-module --init-type" +
		" --init-version --init.author.email --init.author.name" +
		" --init.author.url --init.license --init.module --init.version" +
		" --install-strategy --key --libc --local-address --location" +
		" --lockfile-version --loglevel --logs-dir --logs-max --name" +
		" --maxsockets --message --min-release-age" +
		" --min-release-age-exclude --node-gyp --node-options --noproxy" +
		" --omit --only --orgs --os --otp --package --pack-destination" +
		" --packages --prefix --preid --provenance-file --proxy --registry" +
		" --replace-registry-host --save-prefix --sbom-format --sbom-type" +
		" --scope --scopes --packages-and-scopes-permission" +
		" --orgs-permission --password --token-description --script-shell" +
		" --searchexclude --searchlimit --searchopts --searchstaleness" +
		" --shell --tag --tag-version-prefix --umask --user-agent" +
		" --userconfig --viewer --which --workspace"
	npmConfigValues    = npmShortValues + " " + npmLongValues
	npmBooleanValues   = "true false null"
	npmBooleanLiterals = "--color:always"
)
