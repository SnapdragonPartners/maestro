Take a close look at any new or modified markdown files - those usually represent specs that were implemented as part of this branch. 
As part of your review, try to determine if the spec is fully implemented and/or if the implementation diverges from the spec.
We have some custom coding rules in AGENTS.md that should be considered (e.g. around type assertions).

Beyond these specific points, focus on code correctness and form, especially DRYness. 
This is becoming a complex codebase and reuse is generally a win. Also feel free to suggest new tests. 
The linters are pretty aggressive so form/syntax is less important than correctness, edge cases, testability, and simplicity.
