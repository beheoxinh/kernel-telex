package bamboo

type InputMethodDefinition map[string]string

var InputMethodDefinitions = map[string]InputMethodDefinition{
	"Telex": {
		"z": "XoaDauThanh",
		"s": "DauSac",
		"f": "DauHuyen",
		"r": "DauHoi",
		"x": "DauNga",
		"j": "DauNang",
		"a": "A_Â",
		"e": "E_Ê",
		"o": "O_Ô",
		"w": "UOA_ƯƠĂ",
		"d": "D_Đ",
	},
	"Telex 2": {
		"z": "XoaDauThanh",
		"s": "DauSac",
		"f": "DauHuyen",
		"r": "DauHoi",
		"x": "DauNga",
		"j": "DauNang",
		"a": "A_Â",
		"e": "E_Ê",
		"o": "O_Ô",
		"w": "UOA_ƯƠĂ__Ư",
		"d": "D_Đ",
		"]": "__ư",
		"[": "__ơ",
		"}": "_Ư",
		"{": "_Ơ",
	},
}

const DefaultInputMethodName = "Telex"

func GetInputMethodDefinitions() map[string]InputMethodDefinition {
	var t = make(map[string]InputMethodDefinition)
	for k, v := range InputMethodDefinitions {
		t[k] = v
	}
	return t
}
