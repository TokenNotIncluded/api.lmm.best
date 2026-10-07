package model

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const storeMediaTestSVG = `<svg viewBox="0 0 48 48"><defs><linearGradient id="color"><stop offset="0" stop-color="#24c"/><stop offset="1" stop-color="#7cf"/></linearGradient></defs><rect width="48" height="48" rx="8" fill="url(#color)"/><text x="8" y="30" font-size="12">店铺</text></svg>`

func TestMerchantStoreMediaStaticSVGAndLegacyImages(t *testing.T) {
	image, err := normalizeMerchantStoreImage(storeMediaTestSVG)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(image, MerchantStoreSVGDataPrefix))
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(image, MerchantStoreSVGDataPrefix))
	require.NoError(t, err)
	require.Contains(t, string(decoded), `xmlns="http://www.w3.org/2000/svg"`)
	require.Contains(t, string(decoded), "店铺")
	again, err := normalizeMerchantStoreImage(image)
	require.NoError(t, err)
	require.Equal(t, image, again)
	for _, url := range []string{"https://cdn.example.test/a.png?key=public", "http://legacy.example.test/a.jpg"} {
		actual, err := normalizeMerchantStoreImage(url)
		require.NoError(t, err)
		require.Equal(t, url, actual)
	}
	// A comment must not redirect insertion of the root namespace.
	image, err = normalizeMerchantStoreImage(`<!-- <svg is not the root --> <svg><circle r="5"/></svg>`)
	require.NoError(t, err)
	_, err = normalizeMerchantStoreImage(image)
	require.NoError(t, err)
}

func TestMerchantStoreMediaRejectsActiveExternalAndOversizedSVG(t *testing.T) {
	for name, source := range map[string]string{
		"script":            `<svg><script>alert(1)</script></svg>`,
		"event":             `<svg onload="alert(1)"/>`,
		"nested event":      `<svg><path d="M0 0" onclick="alert(1)"/></svg>`,
		"foreign HTML":      `<svg><foreignObject><div>HTML</div></foreignObject></svg>`,
		"image":             `<svg><image href="https://evil.example/track"/></svg>`,
		"external paint":    `<svg><rect fill="url(https://evil.example/x.svg#p)"/></svg>`,
		"encoded paint":     `<svg><rect fill="u&#114;l(https://evil.example/x)"/></svg>`,
		"CSS escape":        `<svg><rect fill="u\72l(https://evil.example/x)"/></svg>`,
		"CSS comment":       `<svg><rect fill="u/**/rl(//evil.example/x)"/></svg>`,
		"style":             `<svg><style>@import url(https://evil.example/x)</style></svg>`,
		"style attr":        `<svg style="background:url(//evil.example/x)"/>`,
		"base":              `<svg xml:base="https://evil.example/"/>`,
		"use":               `<svg><use href="#loop"/></svg>`,
		"animate":           `<svg><animate attributeName="href" to="evil"/></svg>`,
		"entity":            `<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]><svg>&x;</svg>`,
		"PI":                `<?xml-stylesheet href="https://evil.example/x"?><svg/>`,
		"other namespace":   `<svg xmlns="https://evil.example/"/>`,
		"namespaced attr":   `<svg xmlns:x="http://www.w3.org/1999/xlink" x:href="evil"/>`,
		"duplicate attr":    `<svg width="24" width="99999"/>`,
		"two roots":         `<svg/><svg/>`,
		"text outside":      `text<svg/>`,
		"malformed":         `<svg><path></svg>`,
		"infinite":          `<svg width="NaN"/>`,
		"large dimension":   `<svg width="4097"/>`,
		"large viewBox":     `<svg viewBox="0 0 9000 9000"/>`,
		"depth":             `<svg>` + strings.Repeat(`<g>`, 32) + strings.Repeat(`</g>`, 32) + `</svg>`,
		"nodes":             `<svg>` + strings.Repeat(`<path/>`, 4096) + `</svg>`,
		"bytes":             `<svg><desc>` + strings.Repeat("x", MerchantStoreSVGMaxBytes) + `</desc></svg>`,
		"URI type":          `data:image/svg+xml,<svg/>`,
		"other URI":         `data:text/html;base64,PHN2Zy8+`,
		"base64 whitespace": MerchantStoreSVGDataPrefix + "PHN2\nZy8+",
		"invalid UTF8":      MerchantStoreSVGDataPrefix + base64.StdEncoding.EncodeToString([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := normalizeMerchantStoreImage(source)
			require.ErrorIs(t, err, ErrMerchantStoreInput)
			if strings.HasPrefix(source, "<") {
				_, err = normalizeMerchantStoreImage(MerchantStoreSVGDataPrefix + base64.StdEncoding.EncodeToString([]byte(source)))
				require.ErrorIs(t, err, ErrMerchantStoreInput)
			}
		})
	}
}

func TestMerchantStoreMediaSavePreservesGalleryStockAndGuestVisibility(t *testing.T) {
	f := newStoreFixture(t, "balance")
	var stocks []MerchantStoreStock
	require.NoError(t, DB.Order("id").Find(&stocks).Error)
	input := MerchantStoreProductInput{Title: f.product.Title, PriceQuota: f.product.PriceQuota, PaymentMethods: []string{"balance"}, ImageURLs: []string{storeMediaTestSVG, "https://cdn.example.test/existing.png", "http://legacy.example.test/original.jpg"}}
	p, err := SaveMerchantStoreProduct(f.seller.Id, f.product.ID, input)
	require.NoError(t, err)
	require.Len(t, p.ImageURLs, 3)
	require.True(t, strings.HasPrefix(p.ImageURLs[0], MerchantStoreSVGDataPrefix))
	require.Equal(t, input.ImageURLs[1:], p.ImageURLs[1:])
	require.Equal(t, storeMediaTestSVG, input.ImageURLs[0], "validation must not mutate the caller's gallery")
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, p.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, p.ID, true, ""))
	public, err := GetMerchantStoreProductForViewer(0, p.ID)
	require.NoError(t, err)
	require.Equal(t, p.ImageURLs, public.ImageURLs)
	list, err := ListMerchantStoreProductsForViewer(0, "", 0, 30)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, p.ImageURLs, list[0].ImageURLs)
	var after []MerchantStoreStock
	require.NoError(t, DB.Order("id").Find(&after).Error)
	require.Equal(t, stocks, after)
	bad := input
	bad.ImageURLs = []string{`<svg onload="evil()"/>`}
	_, err = SaveMerchantStoreProduct(f.seller.Id, p.ID, bad)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	var unchanged MerchantStoreProduct
	require.NoError(t, DB.First(&unchanged, "id = ?", p.ID).Error)
	require.Equal(t, p.ImageURLs, unchanged.ImageURLs)
	require.Equal(t, "published", unchanged.Status)
	// SVG support is image-only, never an exception for ordinary product links.
	bad = input
	bad.Links = []MerchantStoreLink{{Title: "not a link", URL: p.ImageURLs[0]}}
	_, err = SaveMerchantStoreProduct(f.seller.Id, p.ID, bad)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
}

func TestMerchantStoreMediaOptionalLogoHeaderPreserveGalleryPositions(t *testing.T) {
	for _, images := range [][]string{
		{"", storeMediaTestSVG, "https://cdn.example.test/gallery.png"},
		{"https://cdn.example.test/logo.png", "", "https://cdn.example.test/gallery.png"},
	} {
		input := MerchantStoreProductInput{Title: "Independent media roles", PriceQuota: 500000, ImageURLs: images}
		require.NoError(t, validateStoreProduct(&input))
		require.Len(t, input.ImageURLs, 3)
		require.Equal(t, images[0], input.ImageURLs[0])
		require.Equal(t, images[2], input.ImageURLs[2])
		if images[1] == "" {
			require.Empty(t, input.ImageURLs[1])
		} else {
			require.True(t, strings.HasPrefix(input.ImageURLs[1], MerchantStoreSVGDataPrefix))
		}
	}
	input := MerchantStoreProductInput{Title: "Not an optional role", PriceQuota: 500000, ImageURLs: []string{"", "", ""}}
	require.ErrorIs(t, validateStoreProduct(&input), ErrMerchantStoreInput)
}

func TestMerchantStoreMediaVisualAnimationRoundTrip(t *testing.T) {
	for name, source := range map[string]string{
		"opacity pulse": `<svg viewBox="0 0 48 48"><rect width="48" height="48"><animate attributeName="opacity" values="0.2;1;0.2" dur="2s" keyTimes="0;0.5;1" repeatCount="indefinite"/></rect></svg>`,
		"rotation":      `<svg><g><animateTransform attributeName="transform" type="rotate" from="0 24 24" to="360 24 24" dur="4s" repeatCount="indefinite"/></g></svg>`,
		"spline motion": `<svg><g><animateTransform attributeName="transform" type="translate" values="0 0;10 5;0 0" dur="1500ms" begin="0.2s" keyTimes="0;0.5;1" calcMode="spline" keySplines="0.4 0 0.6 1;0.4 0 0.6 1"/></g></svg>`,
		"color":         `<svg><rect><animate attributeName="fill" from="#123" to="rebeccapurple" dur="1min" fill="freeze"/></rect></svg>`,
		"gradient stop": `<svg><defs><linearGradient id="g"><stop><animate attributeName="stop-color" values="#123;#456" dur="1s"/></stop></linearGradient></defs></svg>`,
		"offset":        `<svg><path><animate attributeName="stroke-dashoffset" from="-10" to="10" dur="1s" additive="sum" accumulate="none"/></path></svg>`,
		"scale":         `<svg><g><animateTransform attributeName="transform" type="scale" values="1;2;1" dur="2"/></g></svg>`,
		"by":            `<svg><rect><animate attributeName="opacity" by="0.5" dur="2s" attributeType="XML"/></rect></svg>`,
	} {
		t.Run(name, func(t *testing.T) {
			image, err := NormalizeMerchantStoreImage(source)
			require.NoError(t, err)
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(image, MerchantStoreSVGDataPrefix))
			require.NoError(t, err)
			require.Contains(t, string(decoded), "<animate")
			again, err := NormalizeMerchantStoreImage(image)
			require.NoError(t, err)
			require.Equal(t, image, again)
		})
	}
}

func TestMerchantStoreMediaAnimationRejectsMutationEventsAndInvalidValues(t *testing.T) {
	for name, animation := range map[string]string{
		"href mutation":            `<animate attributeName="href" to="javascript:evil()" dur="1s"/>`,
		"style mutation":           `<animate attributeName="style" to="fill:red" dur="1s"/>`,
		"event mutation":           `<animate attributeName="onload" to="evil()" dur="1s"/>`,
		"namespace mutation":       `<animate attributeName="xmlns" to="http://evil.example/" dur="1s"/>`,
		"local URL paint mutation": `<animate attributeName="fill" values="red;url(#g)" dur="1s"/>`,
		"external paint mutation":  `<animate attributeName="fill" to="url(https://evil.example/x)" dur="1s"/>`,
		"event begin":              `<animate attributeName="opacity" to="1" dur="1s" begin="click"/>`,
		"syncbase begin":           `<animate attributeName="opacity" to="1" dur="1s" begin="x.end"/>`,
		"repeat begin":             `<animate attributeName="opacity" to="1" dur="1s" begin="x.repeat(1)"/>`,
		"multi begin":              `<animate attributeName="opacity" to="1" dur="1s" begin="0;click"/>`,
		"external target":          `<animate href="https://evil.example/x" attributeName="opacity" to="1" dur="1s"/>`,
		"local target":             `<animate href="#x" attributeName="opacity" to="1" dur="1s"/>`,
		"event attr":               `<animate onbegin="evil()" attributeName="opacity" to="1" dur="1s"/>`,
		"style attr":               `<animate style="color:red" attributeName="opacity" to="1" dur="1s"/>`,
		"CSS type":                 `<animate attributeType="CSS" attributeName="opacity" to="1" dur="1s"/>`,
		"set":                      `<set attributeName="opacity" to="1" dur="1s"/>`,
		"motion":                   `<animateMotion path="M0 0 L10 10" dur="1s"/>`,
		"nested node":              `<animate attributeName="opacity" to="1" dur="1s"><rect/></animate>`,
		"animation text":           `<animate attributeName="opacity" to="1" dur="1s">not empty</animate>`,
		"no duration":              `<animate attributeName="opacity" to="1"/>`,
		"zero duration":            `<animate attributeName="opacity" to="1" dur="0s"/>`,
		"invalid duration":         `<animate attributeName="opacity" to="1" dur="NaN"/>`,
		"oversized opacity":        `<animate attributeName="opacity" to="2" dur="1s"/>`,
		"infinite value":           `<animate attributeName="opacity" to="1e999" dur="1s"/>`,
		"empty frame":              `<animate attributeName="opacity" values="0;;1" dur="1s"/>`,
		"ambiguous frames":         `<animate attributeName="opacity" values="0;1" to="1" dur="1s"/>`,
		"ambiguous endpoint":       `<animate attributeName="opacity" to="1" by="0.1" dur="1s"/>`,
		"no endpoint":              `<animate attributeName="opacity" from="0" dur="1s"/>`,
		"bad keyTimes":             `<animate attributeName="opacity" values="0;1;0" dur="1s" keyTimes="0;1;0.5"/>`,
		"bad keyTimes count":       `<animate attributeName="opacity" values="0;1;0" dur="1s" keyTimes="0;1"/>`,
		"bad spline count":         `<animate attributeName="opacity" values="0;1;0" dur="1s" calcMode="spline" keySplines="0 0 1 1"/>`,
		"bad spline value":         `<animate attributeName="opacity" from="0" to="1" dur="1s" calcMode="spline" keySplines="0 -1 1 1"/>`,
		"missing spline":           `<animate attributeName="opacity" to="1" dur="1s" calcMode="spline"/>`,
		"invalid transform":        `<animateTransform attributeName="transform" type="rotate" to="10 20" dur="1s"/>`,
		"transform injection":      `<animateTransform attributeName="transform" type="translate" to="url(#g)" dur="1s"/>`,
		"wrong transform target":   `<animateTransform attributeName="href" type="rotate" to="10" dur="1s"/>`,
		"unbounded transform":      `<animateTransform attributeName="transform" type="scale" to="1e999" dur="1s"/>`,
		"stop on shape":            `<animate attributeName="stop-color" to="red" dur="1s"/>`,
		"too many frames":          `<animate attributeName="opacity" values="` + strings.Repeat("0;", 256) + `1" dur="1s"/>`,
	} {
		t.Run(name, func(t *testing.T) {
			source := `<svg><rect>` + animation + `</rect></svg>`
			_, err := NormalizeMerchantStoreImage(source)
			require.ErrorIs(t, err, ErrMerchantStoreInput)
			_, err = NormalizeMerchantStoreImage(MerchantStoreSVGDataPrefix + base64.StdEncoding.EncodeToString([]byte(source)))
			require.ErrorIs(t, err, ErrMerchantStoreInput)
		})
	}
}
