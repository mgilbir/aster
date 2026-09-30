// Package liberation embeds the Liberation Sans, Liberation Mono, and
// Liberation Serif font families.
//
// Liberation Sans is metrically compatible with Arial, Liberation Serif with
// Times New Roman, and Liberation Mono with Courier New.
// See LICENSE in this directory for full copyright and license terms.
package liberation

import _ "embed"

//go:embed LiberationSans-Regular.ttf
var SansRegular []byte

//go:embed LiberationSans-Bold.ttf
var SansBold []byte

//go:embed LiberationSans-Italic.ttf
var SansItalic []byte

//go:embed LiberationSans-BoldItalic.ttf
var SansBoldItalic []byte

//go:embed LiberationMono-Regular.ttf
var MonoRegular []byte

//go:embed LiberationMono-Bold.ttf
var MonoBold []byte

//go:embed LiberationMono-Italic.ttf
var MonoItalic []byte

//go:embed LiberationMono-BoldItalic.ttf
var MonoBoldItalic []byte

//go:embed LiberationSerif-Regular.ttf
var SerifRegular []byte

//go:embed LiberationSerif-Bold.ttf
var SerifBold []byte

//go:embed LiberationSerif-Italic.ttf
var SerifItalic []byte

//go:embed LiberationSerif-BoldItalic.ttf
var SerifBoldItalic []byte
