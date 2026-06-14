<!-- OPENSPEC:START -->
# OpenSpec Instructions

These instructions are for AI assistants working in this project.

Always open `@/openspec/AGENTS.md` when the request:
- Mentions planning or proposals (words like proposal, spec, change, plan)
- Introduces new capabilities, breaking changes, architecture shifts, or big performance/security work
- Sounds ambiguous and you need the authoritative spec before coding

Use `@/openspec/AGENTS.md` to learn:
- How to create and apply change proposals
- Spec format and conventions
- Project structure and guidelines

Keep this managed block so 'openspec update' can refresh the instructions.

<!-- OPENSPEC:END -->

## Git Workflow
- **直接提交到 `main`**：本项目允许在默认分支 `main` 上直接 commit，**不要**为了提交而新建分支（覆盖"if on the default branch, branch first"的内置默认）。仅在用户明确要求时才开分支或 push。

## UI/UX & Aesthetic Standards
- **Style**: Adhere to modern, minimalist, premium SaaS design. Avoid generic, multi-colored Bootstrap/AI looks.
- **Color Palette**: Use a refined, limited color palette. 
  - *Light Mode*: Background `#FAFAFA` (zinc-50), Text `#09090B` (zinc-950).
  - *Dark Mode*: Background `#09090B` (zinc-950), Text `#F4F4F5` (zinc-100).
  - *Accent*: Use ONE primary accent color (e.g., Violet `#7C3AED` or Slate `#475569`), never oversaturate.
- **Typography**: Increase vertical breathing room. Use `tracking-tight` for headings (`font-bold`), and `tracking-normal` with `leading-relaxed` for body text.
- **Spacing & Layout**: Emphasize white space (whitespace is design). Use generous padding (`p-6` to `p-12`) and gaps (`gap-8`). Prefer asymmetric or bento-grid layouts over rigid, uniform tables/grids.
- **Borders & Shadows**: Use ultra-subtle borders (`border-zinc-100` or `dark:border-zinc-800`). Avoid heavy shadows; use soft, multi-layered ambient shadows (`shadow-sm` or custom low-opacity shadows).
- **Interactions**: All interactive elements (buttons, links, cards) MUST have smooth transitions: `transition-all duration-200 ease-out`.
