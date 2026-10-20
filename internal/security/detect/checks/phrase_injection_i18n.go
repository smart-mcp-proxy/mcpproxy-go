package checks

import (
	"regexp"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/detect"
)

// Non-English instruction overrides (SEC-01). The English families above never
// see "ignoriere alle bisherigen Anweisungen" or "忽略之前的所有指令", yet a model
// reads them exactly like "ignore all previous instructions". Each language
// family requires the same three parts as the English hard family — an
// IMPERATIVE override verb, a PRIOR/ORIGINAL qualifier and a DIRECTIVE noun —
// so ordinary prose ("Ignoriert leere Zeilen und überschreibt die vorherigen
// Einstellungen", "忽略空行并返回之前的搜索结果") does not match: third-person
// verbs are not listed and nouns are limited to instruction/rule words.
//
// Patterns run over PhraseText (lowercased, lightly stemmed — "todas"→"toda",
// "instrucciones"→"instrucci…" — hence the \p{L}* prefix stems). Go's \b is
// ASCII-only, so word boundaries are spelled (?:^|[^\p{L}\p{N}]).
//
// All non-English families are SOFT (review only): pattern matching cannot
// see every language's negation ("Ignoriere die vorherigen Anweisungen nicht",
// "以前の指示を無視しないで") or descriptive framing, and the position
// classifier's cues are English. A match therefore HOLDS the change under
// trust_mode=scan (verdict "warnings") but never auto-quarantines a server
// (SEC-01 review rounds 1-2).
//
// Coverage is deliberately bounded to languages written here: de, fr, es, it,
// pt, nl, pl, ru, uk, el, tr, zh, ja, ko. Anything else is a documented gap
// measured by the SEC-01 corpus and the round-2 red-team harness
// (cmd/tpa-redteam: translate-* mutations), not silently claimed. The round-2
// red team (2026-10-20, R2-3) found Turkish and Greek overrides scanning clean
// and added both; Turkish is SOV (object first: "önceki talimatları yoksay"),
// so it has its own family shape below rather than the spaced verb-first one.
// A Turkish system-prompt DISCLOSURE family ("sistem istemini ifşa et") is
// also included, the only non-English disclosure family so far.
//
// Because the de-obfuscated PhraseText view folds diacritics on Latin letters
// (R2-4, detect.NormalizeForPhrase) while the plain NormalizedText view keeps
// them, every Latin-script family here must match BOTH spellings: write the
// accented form AND the bare form (önceki|onceki), or stem past the accented
// letter with \p{L}*.

type i18nOverride struct {
	lang  string
	verb  string // imperative override verbs
	fill  string // determiners / quantifiers / possessives that may sit between
	prior string // previous / original / system qualifiers
	noun  string // directive nouns
}

const i18nBoundary = `(?:^|[^\p{L}\p{N}])`

var i18nOverrides = []i18nOverride{
	{
		lang:  "de",
		verb:  `ignoriere|ignorier|ignorieren sie|vergiss|vergessen sie|missachte|missachten sie|übergehe|übergehen sie|verwirf|verwerfen sie`,
		fill:  `alle|sämtliche|jegliche|jede|die|der|den|deine|ihre|eure|deiner|ihrer|bisher|zuvor|von|diese|meine`,
		prior: `vorherig\p{L}*|bisherig\p{L}*|früher\p{L}*|vorangegangen\p{L}*|vorangehend\p{L}*|obig\p{L}*|vorstehend\p{L}*|ursprünglich\p{L}*|vorig\p{L}*|alten|alte|erhaltenen|gegebenen|system\p{L}*`,
		noun:  `anweisung\p{L}*|instruktion\p{L}*|befehl\p{L}*|regel\p{L}*|vorgabe\p{L}*|richtlinie\p{L}*|anordnung\p{L}*|direktive\p{L}*|weisung\p{L}*|anleitung\p{L}*|prompt\p{L}*|systemprompt\p{L}*`, //nolint:misspell // German/Spanish/Portuguese words, not English typos
	},
	{
		lang:  "fr",
		verb:  `ignore|ignorez|oublie|oubliez|néglige|négligez|ne tiens pas compte d\p{L}*|ne tenez pas compte d\p{L}*|fais abstraction d\p{L}*|faites abstraction d\p{L}*|outrepasse|outrepassez|écarte|écartez`,
		fill:  `tou\p{L}*|les|des|la|le|l'|de|du|tes|vos|ces`,
		prior: `précédent\p{L}*|precedent\p{L}*|antérieur\p{L}*|anterieur\p{L}*|ci-dessus|initia\p{L}*|d'origine|originel\p{L}*|original\p{L}*|reçu\p{L}*|donné\p{L}*|du système|du systeme`,
		noun:  `instruction\p{L}*|consign\p{L}*|directiv\p{L}*|règle\p{L}*|regle\p{L}*|commande\p{L}*|ordre\p{L}*|indication\p{L}*|prompt\p{L}*`,
	},
	{
		lang:  "es",
		verb:  `ignora|ignore|ignoren|ignorad|olvida|olvide|olviden|olvidad|descarta|descarte|descarten|omite|omita|omitan|desobedece|desobedezca|haz caso omiso de|no hagas caso de|no hagas caso a|no sigas|no siga`,
		fill:  `tod\p{L}*|la|lo|las|los|tus|sus|de|del|el|esta|esa|estas|esas|cualquier`,
		prior: `anterior\p{L}*|previ\p{L}*|precedente\p{L}*|de arriba|original\p{L}*|inicial\p{L}*|recibid\p{L}*|del sistema`,
		noun:  `instrucci\p{L}*|indicaci\p{L}*|orden\p{L}*|regla\p{L}*|directiva\p{L}*|comando\p{L}*|pauta\p{L}*|consigna\p{L}*|prompt\p{L}*`, //nolint:misspell // German/Spanish/Portuguese words, not English typos
	},
	{
		lang:  "it",
		verb:  `ignora|ignori|ignorate|dimentica|dimentichi|dimenticate|trascura|trascurate|tralascia|non tenere conto d\p{L}*|non seguire|disobbedisci`,
		fill:  `tutt\p{L}*|le|gli|i|tue|sue|delle|degli|dei|quest\p{L}*|di|qualsiasi`,
		prior: `precedent\p{L}*|anterior\p{L}*|sopra|iniziali|original\p{L}*|di prima|ricevut\p{L}*|del sistema`,
		noun:  `istruzion\p{L}*|indicazion\p{L}*|regol\p{L}*|direttiv\p{L}*|comand\p{L}*|ordin\p{L}*|prompt\p{L}*`,
	},
	{
		lang:  "pt",
		verb:  `ignore|ignora|ignorem|esqueça|esqueca|esquece|esqueçam|desconsidere|desconsidera|despreze|descarte|descarta|não siga|nao siga`,
		fill:  `tod\p{L}*|as|os|a|o|suas|tuas|sua|de|das|dos|estas|esta|quaisquer`,
		prior: `anterior\p{L}*|prévi\p{L}*|previ\p{L}*|acima|original\p{L}*|inicia\p{L}*|recebid\p{L}*|do sistema`,
		noun:  `instru\p{L}*|orienta\p{L}*|ordem|orden\p{L}*|regra\p{L}*|diretiva\p{L}*|diretriz\p{L}*|comando\p{L}*|prompt\p{L}*`, //nolint:misspell // German/Spanish/Portuguese words, not English typos
	},
	{
		lang:  "nl",
		verb:  `negeer|vergeet|ignoreer|veronachtzaam|volg niet`,
		fill:  `alle|al|de|het|je|jouw|uw|deze|die`,
		prior: `eerdere|eerder gegeven|vorige|voorgaande|bovenstaande|oorspronkelijke|originele|ontvangen|systeem\p{L}*`,
		noun:  `instructi\p{L}*|aanwijzing\p{L}*|opdracht\p{L}*|regel\p{L}*|richtlijn\p{L}*|bevel\p{L}*|prompt\p{L}*`,
	},
	{
		lang:  "pl",
		verb:  `zignoruj|ignoruj|zapomnij|pomiń|pomin|zlekceważ|zlekcewaz|nie stosuj się do|nie wykonuj`,
		fill:  `wszystki\p{L}*|wszelki\p{L}*|twoje|swoje|te|o|moje|dotychczas`,
		prior: `poprzedni\p{L}*|wcześniejsz\p{L}*|wczesniejsz\p{L}*|powyższ\p{L}*|powyzsz\p{L}*|dotychczasow\p{L}*|pierwotn\p{L}*|otrzyman\p{L}*|systemow\p{L}*`,
		noun:  `instrukcj\p{L}*|polece\p{L}*|wskazów\p{L}*|wskazow\p{L}*|reguł\p{L}*|regul\p{L}*|zasad\p{L}*|dyrektyw\p{L}*|komend\p{L}*|prompt\p{L}*`,
	},
	{
		lang:  "ru",
		verb:  `игнорируй|игнорируйте|проигнорируй|проигнорируйте|забудь|забудьте|отбрось|отбросьте|не обращай внимания на|не обращайте внимания на|пренебреги|пренебрегите|отмени|отмените|не следуй|не следуйте|не выполняй|не выполняйте`,
		fill:  `все|всё|свои|твои|ваши|эти|любые|данные|ранее`,
		prior: `предыдущ\p{L}*|прежн\p{L}*|предшествующ\p{L}*|вышеуказанн\p{L}*|вышеприведенн\p{L}*|вышеприведённ\p{L}*|изначальн\p{L}*|исходн\p{L}*|полученн\p{L}*|системн\p{L}*|старые|старых`,
		noun:  `инструкци\p{L}*|указани\p{L}*|команд\p{L}*|правил\p{L}*|директив\p{L}*|распоряжени\p{L}*|установк\p{L}*|приказ\p{L}*|промпт\p{L}*`,
	},
	{
		lang:  "uk",
		verb:  `ігноруй|ігноруйте|проігноруй|проігноруйте|забудь|забудьте|відкинь|відкиньте|не виконуй|не виконуйте|не зважай на|не зважайте на`,
		fill:  `всі|усі|свої|твої|ваші|ці|будь-які`,
		prior: `попередн\p{L}*|раніше|вищезазначен\p{L}*|початков\p{L}*|отриман\p{L}*|системн\p{L}*`,
		noun:  `інструкці\p{L}*|вказівк\p{L}*|вказів\p{L}*|команд\p{L}*|правил\p{L}*|директив\p{L}*|промпт\p{L}*`,
	},
	{
		// Greek (R2-3). Verb-first like Spanish: "Αγνόησε όλες τις προηγούμενες
		// οδηγίες". Both tonos and tonos-less spellings are listed because the
		// text may be typed either way (the tonos is a mark on a GREEK letter,
		// so NormalizeForPhrase leaves it in place).
		lang:  "el",
		verb:  `αγνόησε|αγνοησε|αγνοήστε|αγνοηστε|αγνόει|παράβλεψε|παραβλεψε|παραβλέψτε|παραβλεψτε|παράβλεπε|ξέχασε|ξεχασε|ξεχάστε|ξεχαστε|απόρριψε|απορριψε|απορρίψτε|απορριψτε|προσπέρασε|προσπερασε|μην ακολουθήσεις|μην ακολουθησεις|μην ακολουθείτε|μην ακολουθειτε|μη λάβεις υπόψη|μην λάβεις υπόψη|μη λαβεις υποψη|μην λαβεις υποψη|μην υπακούσεις|μην υπακουσεις`,
		fill:  `όλες|ολες|όλα|ολα|όλους|ολους|τις|τα|τους|την|τη|το|τον|σου|σας|τυχόν|τυχον|οποιεσδήποτε|οποιεσδηποτε|κάθε|καθε|πλέον|πλεον`,
		prior: `προηγούμεν\p{L}*|προηγουμεν\p{L}*|παραπάνω|παραπανω|ανωτέρω|ανωτερω|αρχικ\p{L}*|πρότερ\p{L}*|προτερ\p{L}*|προγενέστερ\p{L}*|προγενεστερ\p{L}*|του συστήματος|του συστηματος|συστήματος|συστηματος|που δόθηκαν|που δοθηκαν|που έλαβες|που ελαβες`,
		noun:  `οδηγί\p{L}*|οδηγι\p{L}*|εντολ\p{L}*|κανόν\p{L}*|κανον\p{L}*|υποδείξ\p{L}*|υποδειξ\p{L}*|κατευθυντήρι\p{L}*|κατευθυντηρι\p{L}*|προτροπ\p{L}*|prompt\p{L}*`,
	},
}

// Turkish (R2-3) is SOV: the directive noun and its qualifier come FIRST and
// the imperative verb closes the clause ("Önceki tüm talimatları yoksay",
// "bütün önceki kuralları görmezden gel"). i18nSpacedFamily's verb-first
// shapes cannot express that, so the families below are spelled out. Every
// accented letter is written in both spellings (ö|o, ı|i, ş|s, ç|c, ğ|g, ü|u)
// because the plain view keeps them and the de-obfuscated view folds them
// (R2-4); the dotless ı is additionally folded to i by the confusable step.
var trOverrideFamilies = func() []*regexp.Regexp {
	const (
		// trailing is the consumed trailing word boundary (Go's \b is ASCII-only);
		// phraseFamily.trailingBoundary trims it back out of the evidence.
		trailing = `(?:$|[^\p{L}\p{N}])`
		fill     = `(?:tüm|tum|bütün|butun|verilen|var olan|mevcut|önceden verilen|onceden verilen|sana verilen|size verilen|yukarıdaki|yukaridaki) `
		prior    = `(?:önceki|onceki|daha önceki|daha onceki|evvelki|önceden|onceden|yukarıdaki|yukaridaki|yukarıda verilen|yukarida verilen|ilk|orijinal|orjinal|asıl|asil|sistem|sistemin|mevcut)`
		noun     = `(?:talimat|komut|yönerge|yonerge|kural|direktif|yönlendirme|yonlendirme|istem|prompt|emir|buyruk)\p{L}*`
		// Verbs in the imperative only — 2nd person singular ("yoksay") or
		// plural/formal ("yoksayın", "yoksayınız", "unutun") — plus the negative
		// imperatives that ARE the override ("dikkate alma", "uyma"). Turkish
		// negates with a suffix (-ma/-me, -maz/-mez: "yoksaymaz" = does not
		// ignore; "yoksayar" = ignores), so the verb is NOT stemmed with \p{L}*
		// and must end at a word boundary: ordinary descriptive prose ("önceki
		// talimatları yoksaymaz") stays clean.
		verb = `(?:(?:yoksay|yok say|görmezden gel|gormezden gel|unut|göz ardı et|goz ardi et|iptal et|geçersiz kıl|gecersiz kil|geçersiz say|gecersiz say|bir kenara bırak|bir kenara birak|boş ver|bos ver|boşver|bosver|atla|hiçe say|hice say|reddet)(?:[ıi]n(?:[ıi]z)?|un(?:uz)?|ün(?:üz)?)?|(?:dikkate alma|uyma|takma|umursama|uygulama|izleme)(?:y[ıi]n(?:[ıi]z)?)?)`
		gap  = `(?: \p{L}+){0,3} `
	)
	return []*regexp.Regexp{
		// [fill] PRIOR [fill] NOUN … VERB  — "önceki tüm talimatları yoksay",
		// "tüm önceki kuralları görmezden gel", "sistem talimatlarını unut".
		regexp.MustCompile(i18nBoundary + `(?:` + fill + `)*` + prior + ` (?:` + fill + `)*` + noun + gap + verb + trailing),
		// VERB-first is unidiomatic but attackers write machine translations:
		// "yoksay önceki talimatları".
		regexp.MustCompile(i18nBoundary + verb + ` (?:` + fill + `)*` + prior + ` (?:` + fill + `)*` + noun + trailing),
	}
}()

// trDisclosureFamily is the Turkish counterpart of the English system-prompt
// exfiltration family ("reveal your system prompt"): "sistem istemini ifşa
// et", "sistem komutlarını göster/yazdır/paylaş". Object first, verb last; the
// verb carries only imperative suffixes and ends at a word boundary, for the
// same negation reason as trOverrideFamilies.
var trDisclosureFamily = regexp.MustCompile(i18nBoundary +
	`(?:sistem|gizli|başlangıç|baslangic|ilk) (?:istem|prompt|komut|talimat|yönerge|yonerge|mesaj|yönlendirme|yonlendirme)\p{L}*(?: \p{L}+){0,3} ` +
	`(?:ifşa et|ifsa et|göster|goster|yazdır|yazdir|açıkla|acikla|paylaş|paylas|söyle|soyle|ortaya çıkar|ortaya cikar|çıktı olarak ver|cikti olarak ver|aktar|tekrarla|sızdır|sizdir)(?:[ıi]n(?:[ıi]z)?|un(?:uz)?|ün(?:üz)?|y[ıi]n(?:[ıi]z)?)?(?:$|[^\p{L}\p{N}])`)

// i18nSpacedFamily builds both word orders for a space-delimited language:
// VERB [fill…] PRIOR [word] NOUN  ("ignoriere alle bisherigen Anweisungen") and
// VERB [fill…] NOUN [fill] PRIOR  ("ignora todas las instrucciones anteriores").
func i18nSpacedFamily(o i18nOverride) *regexp.Regexp {
	fill := `(?: (?:` + o.fill + `))*`
	adjFirst := `(?:` + o.prior + `)(?: \p{L}+)? (?:` + o.noun + `)`
	nounFirst := `(?:` + o.noun + `)(?: (?:` + o.fill + `))? (?:` + o.prior + `)`
	return regexp.MustCompile(i18nBoundary + `(?:` + o.verb + `)` + fill + ` (?:` + adjFirst + `|` + nounFirst + `)`)
}

// cjkGap is a short run with no sentence/clause punctuation, binding the verb,
// qualifier and noun to one clause.
const cjkGap = `[^，。；！？、,.;!?\n]`

// cjkOverrideFamilies cover Chinese (verb-first and object-first), Japanese
// (object-first: 以前の指示を…無視) and Korean (object-first: 이전의 … 지시를 무시).
var cjkOverrideFamilies = []struct {
	lang string
	re   *regexp.Regexp
}{
	{"zh", regexp.MustCompile(`(?:忽略|忽视|忽視|无视|無視|忘记|忘記|忘掉|不要理会|不要理會|不理会|不理會|抛弃|拋棄|放弃|放棄|跳过|跳過|不要遵守|不要遵循)` + cjkGap + `{0,8}?(?:之前|以前|先前|此前|上面|以上|上述|前面|原来|原來|原有|最初|系统|系統|前述)` + cjkGap + `{0,6}?(?:指令|指示|提示词|提示詞|提示|规则|規則|命令|设定|設定|约束|約束|要求|指引)`)},
	{"zh", regexp.MustCompile(`(?:之前|以前|先前|此前|上面|以上|上述|前面|原来|原來|原有|最初|系统|系統)` + cjkGap + `{0,4}?(?:指令|指示|提示词|提示詞|提示|规则|規則|命令|设定|設定|约束|約束)` + cjkGap + `{0,8}?(?:忽略|忽视|忽視|无视|無視|忘记|忘記|忘掉|作废|作廢)`)},
	{"ja", regexp.MustCompile(`(?:以前|これまで|今まで|前回|上記|先ほど|先程|最初|元|前|既存|システム)の?` + cjkGap + `{0,6}?(?:指示|命令|指令|ルール|規則|プロンプト|制約|指図)` + cjkGap + `{0,12}?(?:無視|忘れ|破棄|放棄|従わな)`)},
	{"ko", regexp.MustCompile(`(?:이전|앞의|앞서|위의|위|기존|처음|원래|시스템)` + cjkGap + `{0,8}?(?:지시|지침|명령|규칙|프롬프트|안내)` + cjkGap + `{0,10}?(?:무시|잊어|잊으|따르지)`)},
}

// i18nOverrideBase keeps non-English override matches on the SOFT review tier
// (below phraseHardMinConfidence, above phraseSoftFloor).
const i18nOverrideBase = 0.55

// i18nSoftReason is shown in the finding detail.
const i18nSoftReason = "non-English matching cannot rule out negation or descriptive use"

// multilingualOverrideFamilies returns the SOFT phrase families for the
// non-English overrides.
func multilingualOverrideFamilies() []phraseFamily {
	out := make([]phraseFamily, 0, len(i18nOverrides)+len(cjkOverrideFamilies))
	for _, o := range i18nOverrides {
		out = append(out, phraseFamily{
			re:              i18nSpacedFamily(o),
			base:            i18nOverrideBase,
			threat:          detect.ThreatPromptInjection,
			what:            "non-English (" + o.lang + ") instruction-override directive",
			leadingBoundary: true,
			softReason:      i18nSoftReason,
		})
	}
	for _, c := range cjkOverrideFamilies {
		out = append(out, phraseFamily{
			re:         c.re,
			base:       i18nOverrideBase,
			softReason: i18nSoftReason,
			threat:     detect.ThreatPromptInjection,
			what:       "non-English (" + c.lang + ") instruction-override directive",
		})
	}
	for _, re := range trOverrideFamilies {
		out = append(out, phraseFamily{
			re:               re,
			base:             i18nOverrideBase,
			threat:           detect.ThreatPromptInjection,
			what:             "non-English (tr) instruction-override directive",
			leadingBoundary:  true,
			trailingBoundary: true,
			softReason:       i18nSoftReason,
		})
	}
	out = append(out, phraseFamily{
		re:               trDisclosureFamily,
		base:             i18nOverrideBase,
		threat:           detect.ThreatPromptInjection,
		what:             "non-English (tr) system-prompt disclosure directive",
		leadingBoundary:  true,
		trailingBoundary: true,
		softReason:       i18nSoftReason,
	})
	return out
}
