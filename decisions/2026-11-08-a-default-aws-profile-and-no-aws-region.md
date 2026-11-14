# A default AWS profile, and no --aws-region

`[aws] default_profile` is written as `[default]` and must be a granted profile; one granted profile becomes the
default. `stage up --aws-profile` grants one host profile to one sandbox. There is no `--aws-region`: a sandbox has
one region for every profile, so a global override would move a book's profiles too. Evidence:
`TestAWSDefaultProfileMustBeGranted`, `TestTheOnlyGrantedAWSProfileBecomesTheDefault`,
`TestTheAWSProfileFlagIsGrantedAndOverridesEveryOtherDefault`.
