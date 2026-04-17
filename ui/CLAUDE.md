@AGENTS.md

# ExoHash Play — Frontend

Next.js 16 + Tailwind CSS + TypeScript. Dark-theme crypto casino UI.
BFF proxy: `/api/bff/*` rewrites to `localhost:3100`.

## Frontend Aesthetics (MANDATORY)

You MUST follow these rules for ALL frontend code in this project:

<frontend_aesthetics>
You tend to converge toward generic, "on distribution" outputs. In frontend design, this creates what users call the "AI slop" aesthetic. Avoid this: make creative, distinctive frontends that surprise and delight. Focus on:

Typography: Choose fonts that are beautiful, unique, and interesting. Avoid generic fonts like Arial and Inter; opt instead for distinctive choices that elevate the frontend's aesthetics.

Color & Theme: Commit to a cohesive aesthetic. Use CSS variables for consistency. Dominant colors with sharp accents outperform timid, evenly-distributed palettes. Draw from IDE themes and cultural aesthetics for inspiration.

Motion: Use animations for effects and micro-interactions. Prioritize CSS-only solutions for HTML. Use Motion library for React when available. Focus on high-impact moments: one well-orchestrated page load with staggered reveals (animation-delay) creates more delight than scattered micro-interactions.

Backgrounds: Create atmosphere and depth rather than defaulting to solid colors. Layer CSS gradients, use geometric patterns, or add contextual effects that match the overall aesthetic.

Avoid generic AI-generated aesthetics:
- Overused font families (Inter, Roboto, Arial, system fonts)
- Clichéd color schemes (particularly purple gradients on white backgrounds)
- Predictable layouts and component patterns
- Cookie-cutter design that lacks context-specific character
- Flat monotone backgrounds with no depth
- Low-contrast text (gray-500 on near-black is UNREADABLE)

CRITICAL RULES FOR THIS PROJECT:
- Background: gradient from #1a1d27 (top) to #0b0d12 (bottom), never flat
- Accent color: #c8f547 (lime/chartreuse) for CTAs, badges, highlights
- Text: white (#ffffff) for headings, gray-300 (#d1d5db) for body, gray-400 (#9ca3af) for secondary
- Cards: bg-black/40 backdrop-blur with border-white/10, NOT invisible borders
- Buttons: rounded-full pill style with clear borders or solid fills
- ALWAYS verify output with `npx playwright screenshot` before declaring done
- ALWAYS compare screenshot against reference image if one exists
- If the screenshot looks bad, FIX IT before showing to user
</frontend_aesthetics>
