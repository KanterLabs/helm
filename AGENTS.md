# Agent testing policy

- NEVER write unit tests after writing the code they cover.
- Strongly prefer end-to-end tests as the sole testing mechanism for complex features. Verify the real user flow and produce a verifiable, repeatable artifact at the end of each end-to-end test, such as a screenshot, trace, or captured response attached to the test run.
- When a system must be tested in isolation, first write down all the ways it could fail, then write the code and its isolation tests.
- Keep test artifacts free of credentials and private user content.
