# Console navigation and mobile review

Page-level categories use `TabsList variant="navigation"`: one horizontal row,
44px targets, an underline for the selected page, and native horizontal scrolling.
Do not turn five or more categories into a wrapped, fully rounded segmented control.
Keep the default segmented style for small local switches, and preserve vertical tabs.

The mobile console has one footer row: existing navigation and the labelled service
information disclosure. Service terms, project links, and privacy controls remain
available through that disclosure. Keep the same consent component mounted across
viewport changes. The footer follows the shared scroll controller; an open disclosure
or keyboard-focused control must not disappear while being used.

Bounties show the title, primary create action, page categories, and actual projects
before long funding explanations. The funding disclosure retains every payment and
fee explanation. Only show a numeric fee when the server supplied it; unknown or
failed prices must not look like a zero fee. Do not alter payment authorization or
settlement for visual changes.

`console-page-review.mjs` captures every listed static user/admin/settings route at
390px and 1440px in both themes. `sitewide-polish-review.mjs` exercises bounty navigation,
funding disclosures, unobscured privacy controls and scroll recovery at 320–1440px,
including long English labels. `mobile-experience-review.mjs` also reviews public,
authentication and error pages at 320px, 390px and 1440px in both themes.

Run these through the read-only `Sitewide UI review` GitHub Actions workflow. Its
artifacts contain the exact revision, individual page captures, geometry reports,
and check logs. The scripts use isolated review fixtures and reject production
requests. A successful capture is not production deployment or complete validation
of every business action and dynamic detail page.
