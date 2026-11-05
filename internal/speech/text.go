package speech

import (
	"strings"
	"unicode"
)

// Language is a language the person can say they speak in.
type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Languages are the common ones offered in Settings (whisper knows ~100;
// any two-letter code works through the API).
var Languages = []Language{
	{"bs", "Bosnian"}, {"hr", "Croatian"}, {"sr", "Serbian"}, {"sl", "Slovenian"}, {"mk", "Macedonian"},
	{"en", "English"}, {"de", "German"}, {"fr", "French"}, {"it", "Italian"}, {"es", "Spanish"}, {"pt", "Portuguese"},
	{"nl", "Dutch"}, {"sv", "Swedish"}, {"no", "Norwegian"}, {"da", "Danish"}, {"pl", "Polish"}, {"cs", "Czech"},
	{"sk", "Slovak"}, {"hu", "Hungarian"}, {"ro", "Romanian"}, {"bg", "Bulgarian"}, {"el", "Greek"}, {"tr", "Turkish"},
	{"ru", "Russian"}, {"uk", "Ukrainian"}, {"ar", "Arabic"}, {"he", "Hebrew"}, {"hi", "Hindi"}, {"zh", "Chinese"},
	{"ja", "Japanese"}, {"ko", "Korean"},
}

// LanguageName is the English name of a code ("" when unknown).
func LanguageName(code string) string {
	for _, l := range Languages {
		if l.Code == code {
			return l.Name
		}
	}
	return ""
}

// ValidLanguage accepts "" (find out) or a two/three-letter code.
func ValidLanguage(code string) bool {
	if code == "" {
		return true
	}
	if len(code) < 2 || len(code) > 3 {
		return false
	}
	for _, r := range code {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

var cyrLatin = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'ђ': "đ", 'е': "e", 'ж': "ž", 'з': "z", 'и': "i", 'ј': "j", 'к': "k",
	'л': "l", 'љ': "lj", 'м': "m", 'н': "n", 'њ': "nj", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'ћ': "ć", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "c", 'ч': "č", 'џ': "dž", 'ш': "š",
	// Russian letters whisper sometimes slips in
	'й': "j", 'ы': "i", 'э': "e", 'ю': "ju", 'я': "ja", 'щ': "šč", 'ь': "", 'ъ': "", 'ё': "jo",
}

// HasCyrillic reports whether the text has Cyrillic letters.
func HasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// ToLatin writes Serbian (and Bosnian, Macedonian-ish) Cyrillic in Latin
// letters, the way most people here type: "Пробудио сам се" → "Probudio sam se".
func ToLatin(s string) string {
	if !HasCyrillic(s) {
		return s
	}
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		lower := unicode.ToLower(r)
		lat, ok := cyrLatin[lower]
		if !ok {
			b.WriteRune(r)
			continue
		}
		if r != lower && lat != "" {
			// Љ → Lj before a lower-case letter, LJ in a word in capitals
			next := i+1 < len(rs) && unicode.IsUpper(rs[i+1])
			lr := []rune(lat)
			if next || len(lr) == 1 {
				lat = strings.ToUpper(lat)
			} else {
				lat = strings.ToUpper(string(lr[0])) + string(lr[1:])
			}
		}
		b.WriteString(lat)
	}
	return b.String()
}

var fold = strings.NewReplacer("č", "c", "ć", "c", "đ", "dj", "š", "s", "ž", "z", "ä", "a", "ö", "o", "ü", "u", "ß", "ss",
	"é", "e", "è", "e", "ê", "e", "á", "a", "à", "a", "â", "a", "í", "i", "ó", "o", "ô", "o", "ú", "u", "ñ", "n", "ç", "c", "ø", "o", "å", "a")

// Fold is the text as searched: lower case, Latin letters, no accents —
// "Ćevapi", "ćevapi" and "ЋЕВАПИ" all find "cevapi".
func Fold(s string) string {
	return fold.Replace(strings.ToLower(ToLatin(s)))
}
