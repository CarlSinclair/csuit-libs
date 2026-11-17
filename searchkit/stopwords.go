package searchkit

import "strings"

var stopwordLists = map[string]string{
	"en": "a an and are as at be but by can could do does for from had has have how i if in is it its me my of on or our so than that the their then there these they this to was we what when where which who why will with would you your",
	"pt": "a as o os um uma uns umas de da das do dos em na nas no nos por para com sem sobre entre e ou mas que se eu me meu minha meus minhas voce voces nos como quando onde qual quais quem porque ao aos ha foi ser estar tem tenho",
	"es": "a el la los las un una unos unas de del en por para con sin sobre entre y o pero que si yo me mi mis tu tus usted ustedes nosotros como cuando donde cual cuales quien porque al es son fue ser estar tiene tengo",
	"fr": "a au aux le la les un une des de du en par pour avec sans sur entre et ou mais que qui si je me mon ma mes tu vous nous comment quand ou quel quelle quels quelles pourquoi est sont etre avoir ai",
	"it": "a al alla alle il lo la i gli le un uno una di del della dei delle in per con senza su tra fra e o ma che se io mi mio mia miei tu voi noi come quando dove quale quali chi perche sono essere ho",
	"ru": "и в во не что он на я с со как а то все она так его но да ты к у же вы за бы по только ее мне было вот от меня еще нет о из ему теперь когда даже ну вдруг ли если уже или ни быть был него до вас нибудь опять уж вам ведь там потом себя ничего ей может они тут где есть надо ней для мы тебя их чем была сам чтоб без будто чего раз тоже себе под будет",
}

var stopwordSets = func() map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(stopwordLists))
	for lang, raw := range stopwordLists {
		set := map[string]bool{}
		for _, w := range strings.Fields(raw) {
			set[Fold(w)] = true
		}
		out[lang] = set
	}
	return out
}()

// RemoveStopwords drops common function words for the language (a code such
// as "pt" or "pt-BR") from words that were produced by Words. If every word is
// a stopword the input is returned unchanged, so a question made only of
// short words still searches for something. Languages without a list keep
// all their words.
func RemoveStopwords(words []string, lang string) []string {
	lang = strings.ToLower(lang)
	if i := strings.IndexAny(lang, "-_"); i > 0 {
		lang = lang[:i]
	}
	set := stopwordSets[lang]
	if len(set) == 0 {
		return words
	}
	kept := make([]string, 0, len(words))
	for _, w := range words {
		if !set[w] {
			kept = append(kept, w)
		}
	}
	if len(kept) == 0 {
		return words
	}
	return kept
}
