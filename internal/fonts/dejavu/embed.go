// Package dejavu embeds the DejaVu Sans and DejaVu Sans Mono font families.
//
// DejaVu fonts are based on Bitstream Vera fonts.
// See LICENSE in this directory for full copyright and license terms.
package dejavu

import _ "embed"

//go:embed DejaVuSans.ttf
var SansRegular []byte

//go:embed DejaVuSans-Bold.ttf
var SansBold []byte

//go:embed DejaVuSans-Oblique.ttf
var SansOblique []byte

//go:embed DejaVuSans-BoldOblique.ttf
var SansBoldOblique []byte

//go:embed DejaVuSansMono.ttf
var MonoRegular []byte

//go:embed DejaVuSansMono-Bold.ttf
var MonoBold []byte

//go:embed DejaVuSansMono-Oblique.ttf
var MonoOblique []byte

//go:embed DejaVuSansMono-BoldOblique.ttf
var MonoBoldOblique []byte
