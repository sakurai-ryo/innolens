package innodb

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// beUint reads a big-endian unsigned integer of 1..8 bytes.
func beUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

func decUint(b []byte, st *[]Step) string {
	s := strconv.FormatUint(beUint(b), 10)
	if st != nil {
		*st = append(*st, Step{"raw", hex.EncodeToString(b)}, Step{"value", s})
	}
	return s
}

// decSint decodes an InnoDB signed integer: big-endian with the sign bit flipped.
func decSint(b []byte, st *[]Step) string {
	bits := uint(len(b)) * 8
	v := beUint(b) ^ (1 << (bits - 1))
	// sign-extend
	if v&(1<<(bits-1)) != 0 {
		v |= ^uint64(0) << bits
	}
	s := strconv.FormatInt(int64(v), 10)
	if st != nil {
		*st = append(*st,
			Step{"raw", hex.EncodeToString(b)},
			Step{"sign bit flipped", fmt.Sprintf("%0*x", len(b)*2, v&(1<<bits-1))},
			Step{"value", s},
		)
	}
	return s
}

func intDecoder(unsigned bool) func([]byte, *[]Step) string {
	if unsigned {
		return decUint
	}
	return decSint
}

// decDate decodes MYSQL_TYPE_NEWDATE: 3-byte signed int, day(5) month(4) year(15).
func decDate(b []byte, st *[]Step) string {
	v := beUint(b) ^ 0x800000
	s := fmt.Sprintf("%04d-%02d-%02d", v>>9, (v>>5)&0xF, v&0x1F)
	if st != nil {
		*st = append(*st,
			Step{"raw", hex.EncodeToString(b)},
			Step{"sign bit flipped", fmt.Sprintf("%06x", v)},
			Step{"bit-packed", fmt.Sprintf("year %d (15 bits) | month %d (4) | day %d (5)", v>>9, (v>>5)&0xF, v&0x1F)},
			Step{"value", s},
		)
	}
	return s
}

func fracBytes(fsp int) int { return (fsp + 1) / 2 }

func decFrac(b []byte, fsp int) string {
	if fsp == 0 {
		return ""
	}
	n := fracBytes(fsp)
	us := beUint(b[len(b)-n:])
	// fractional part is stored as microseconds / 10^(6-fsp) rounded to the byte width
	for i := n * 2; i < 6; i++ {
		us *= 10
	}
	return fmt.Sprintf(".%06d", us)[:fsp+1]
}

// decDatetime decodes DATETIME2: 5 bytes (1 sign, 17 year*13+month, 5 day, 5 hour, 6 min, 6 sec) + fraction.
func decDatetime(fsp int) func([]byte, *[]Step) string {
	return func(b []byte, st *[]Step) string {
		v := beUint(b[:5]) ^ 0x8000000000
		ym := (v >> 22) & 0x1FFFF
		s := fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d%s", ym/13, ym%13, (v>>17)&0x1F, (v>>12)&0x1F, (v>>6)&0x3F, v&0x3F, decFrac(b, fsp))
		if st != nil {
			*st = append(*st,
				Step{"raw", hex.EncodeToString(b)},
				Step{"sign bit flipped", fmt.Sprintf("%010x", v)},
				Step{"bit-packed", fmt.Sprintf("year*13+month %d (17 bits) | day %d (5) | hour %d (5) | min %d (6) | sec %d (6)",
					ym, (v>>17)&0x1F, (v>>12)&0x1F, (v>>6)&0x3F, v&0x3F)},
			)
			if fsp > 0 {
				*st = append(*st, Step{"fraction", fmt.Sprintf("%s (fsp %d, %d bytes)", decFrac(b, fsp), fsp, fracBytes(fsp))})
			}
			*st = append(*st, Step{"value", s})
		}
		return s
	}
}

// decTimestamp decodes TIMESTAMP2: 4-byte big-endian unix seconds + fraction, shown in UTC.
func decTimestamp(fsp int) func([]byte, *[]Step) string {
	return func(b []byte, st *[]Step) string {
		sec := beUint(b[:4])
		s := time.Unix(int64(sec), 0).UTC().Format("2006-01-02 15:04:05") + decFrac(b, fsp) + " UTC"
		if st != nil {
			*st = append(*st,
				Step{"raw", hex.EncodeToString(b)},
				Step{"unix seconds", strconv.FormatUint(sec, 10)},
			)
			if fsp > 0 {
				*st = append(*st, Step{"fraction", fmt.Sprintf("%s (fsp %d, %d bytes)", decFrac(b, fsp), fsp, fracBytes(fsp))})
			}
			*st = append(*st, Step{"value", s})
		}
		return s
	}
}

var dig2bytes = [...]int{0, 1, 1, 2, 2, 3, 3, 4, 4, 4}

func decimalSize(precision, scale int) int {
	intg := precision - scale
	return intg/9*4 + dig2bytes[intg%9] + scale/9*4 + dig2bytes[scale%9]
}

// decDecimal decodes the my_decimal binary format (decimal2bin).
func decDecimal(precision, scale int) func([]byte, *[]Step) string {
	return func(b []byte, st *[]Step) string {
		if len(b) != decimalSize(precision, scale) {
			return hex.EncodeToString(b)
		}
		buf := append([]byte(nil), b...)
		neg := buf[0]&0x80 == 0
		buf[0] ^= 0x80
		if neg {
			for i := range buf {
				buf[i] = ^buf[i]
			}
		}
		intg := precision - scale
		var sb strings.Builder
		if neg {
			sb.WriteByte('-')
		}
		pos := 0
		group := func(digits int) {
			n := dig2bytes[digits%9]
			if digits >= 9 {
				n = 4
			}
			v := beUint(buf[pos : pos+n])
			pos += n
			fmt.Fprintf(&sb, "%0*d", digits, v)
		}
		if intg%9 > 0 {
			group(intg % 9)
		}
		for i := 0; i < intg/9; i++ {
			group(9)
		}
		if intg == 0 {
			sb.WriteByte('0')
		}
		if scale > 0 {
			sb.WriteByte('.')
			for i := 0; i < scale/9; i++ {
				group(9)
			}
			if scale%9 > 0 {
				group(scale % 9)
			}
		}
		s := sb.String()
		// leading zeros are an artefact of the fixed-width groups
		s = strings.TrimLeft(s, "-0")
		if s == "" || s[0] == '.' {
			s = "0" + s
		}
		if neg {
			s = "-" + s
		}
		if st != nil {
			sign := "positive"
			if neg {
				sign = "negative (bytes complemented)"
			}
			*st = append(*st,
				Step{"raw", hex.EncodeToString(b)},
				Step{"sign bit flipped", hex.EncodeToString(buf)},
				Step{"sign", sign},
				Step{"digit groups", fmt.Sprintf("integer %d digits / %d B | fraction %d digits / %d B",
					intg, intg/9*4+dig2bytes[intg%9], scale, scale/9*4+dig2bytes[scale%9])},
				Step{"value", s},
			)
		}
		return s
	}
}

func strDecoder(charset string) func([]byte, *[]Step) string {
	switch charset {
	case "utf8mb4", "ascii":
		return func(b []byte, _ *[]Step) string {
			if !utf8.Valid(b) {
				return hex.EncodeToString(b)
			}
			return strconv.Quote(string(b))
		}
	case "latin1":
		return func(b []byte, _ *[]Step) string {
			r := make([]rune, len(b))
			for i, c := range b {
				r[i] = rune(c)
			}
			return strconv.Quote(string(r))
		}
	case "binary":
		return func(b []byte, _ *[]Step) string { return hex.EncodeToString(b) }
	default:
		return nil
	}
}

// charsetName maps the collation ids of the charsets we can print.
func charsetName(collationID int) string {
	switch {
	case collationID == 45 || collationID == 46 || (collationID >= 224 && collationID <= 247) || (collationID >= 255 && collationID <= 330):
		return "utf8mb4"
	case collationID == 11 || collationID == 65:
		return "ascii"
	case collationID == 63:
		return "binary"
	}
	switch collationID {
	case 5, 8, 15, 31, 47, 48, 49, 94:
		return "latin1"
	}
	return ""
}

// mbMinLen is 1 for every charset except the fixed-width UCS ones.
func mbMinLen(collationID int) int {
	switch {
	case collationID == 35 || collationID == 90 || (collationID >= 128 && collationID <= 151) || collationID == 159:
		return 2 // ucs2
	case collationID == 54 || collationID == 55 || collationID == 56 || collationID == 62 || (collationID >= 101 && collationID <= 124):
		return 2 // utf16, utf16le
	case collationID == 60 || collationID == 61 || (collationID >= 160 && collationID <= 183):
		return 4 // utf32
	}
	return 1
}
