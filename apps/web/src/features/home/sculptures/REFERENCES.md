# Sculpture references

## Smiling portrait

Input: the user-supplied portrait in the homepage request of 2026-10-10 (UTC+8). The original is not added to the repository. The image is 1536 × 1536; landmark coordinates below use a 1248 × 1248 reference space, not the displayed preview size.

Observed features: short dark hair, a tilted head, broad dark eyebrows, a visible tooth smile and a dark round-neck shirt. The wall is excluded. No identity is assigned to the subject.

The runtime uses a subject-masked, 29-color material grid (204 × 196 samples) and a sculpted face surface with nose/cheek depth, an approximate rear cap, a continuous neck and independently weighted mouth/eyelids. The sampling rectangle in reference space is x=160, y=200, width=1088, height=1048. Deterministic sub-cell sampling prevents large aligned grid gaps. Dark materials are raised slightly to retain the shirt/hair silhouette on a dark ground.

This is a stylized **single-view relief reconstruction**, not a scanned, freely rotatable 360-degree likeness. Unseen anatomy cannot be recovered from this one image. Camera yaw is deliberately small. The original image is not decoded, requested or displayed by the page; the only stored reference-derived asset is the compact subject material table. The existing renderer provides the point brush, lighting-mode conversion, pointer/touch response and morph.

The expression has an 8.4-second local loop: smile strength, cheek relaxation, brief lid closure, a small head turn and breathing. Eyelids have a skin underlay so closing the eyes does not reveal the page background. The first display slot is `HOME_SEQUENCES[0][0]`; the whale follows until a second reference is actually available.

## Blue whale

Primary references consulted:

- National Park Service, “Blue Whale”, appearance section and NOAA reference photograph: https://www.nps.gov/places/blue-whale.htm
- NOAA Fisheries, “Blue Whale”: https://www.fisheries.noaa.gov/species/blue-whale

Applied features: a wide, flattened head; a small dorsal fin far toward the tail; paired long flippers; horizontal flukes with a central notch; mottled blue-grey material; lighter underside; representative ventral pleats and paired blowholes. The fins, eye and mouth contours are sampled from the body surface to avoid floating details. Tail movement is vertical. Dimensions and pleat count are stylized for the particle budget, not a scale anatomical reconstruction.

## Pending second reference

Requested video: https://video.twimg.com/tweet_video/HTi44a_aAAAHTz4.mp4

The supplied link could not be read in this session. Its subject and motion have **not** been inspected. No replacement or blank scene is registered. Obtain the MP4 upload, inspect keyframes and then add the confirmed subject as the second entry. Do not label the existing whale as an implementation of this video.
