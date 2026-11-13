package mask

import "strings"

// Built-in name rules (spec "Name rules"). Token rules match a contiguous
// run of the normalized name's tokens; exact rules match the whole name.
// Every entry is normalized with normalizeName when the rule set is built.

const nameRules = `
first_name firstname given_name last_name lastname family_name surname middle_name
maiden_name full_name fullname patronymic vorname nachname familienname geburtsname
prenom nom_famille nom_de_famille imie nazwisko imya familiya otchestvo fio pib
prizvyshche imia po_batkovi имя фамилия отчество фио ім_я прізвище по_батькові піб

birth birthday birthdate dob date_of_birth birth_place geburtsdatum geburtstag geburtsort
naissance date_naissance lieu_naissance urodzenia data_urodzenia miejsce_urodzenia
rozhdeniya data_rozhdeniya mesto_rozhdeniya дата_рождения место_рождения

email e_mail mail phone telephone mobile cell msisdn fax telefon handy mobil portable
komorka телефон

address street zip zipcode postcode postal_code adresse anschrift strasse hausnummer plz
code_postal rue adres ulica kod_pocztowy ulitsa адрес улица

latitude longitude geo geolocation coordinates ip ip_address ipv4 ipv6 mac_address user_agent

ssn tax_id taxid national_id passport id_card driver_license driving_licence steuer_id
steuerid steuernummer sv_nummer rvnr versicherungsnummer ausweis personalausweis reisepass
nir numero_secu securite_sociale numero_fiscal spi siren siret cni pesel nip regon dowod
dowod_osobisty paszport inn snils ogrnip oms polis pasport паспорт инн снилс iin иин
rnokpp ipn unzr pinfl jshshir personal_number fin

iban card_number cardnumber pan cvv cvc account_number bank_account kontonummer
numer_konta rib

password passwd pwd secret token api_key apikey private_key otp pin pin_code
`

const exactNameRules = `
f i o nom tel ort wohnort indeks lat lon lng
`

// specialRules are GDPR Art. 9 categories: redacted, never hashed.
const specialRules = `
diagnosis diagnose diagnostic diagnoza diagnoz health medical disease illness krankheit
maladie choroba religion konfession wyznanie religiya ethnicity ethnic race
nationality_ethnic sexual_orientation orientation biometric biometrics fingerprint face_id
blood_type blutgruppe groupe_sanguin grupa_krwi union_member trade_union gewerkschaft
syndicat
`

// ruleSet is a set of token-sequence rules plus exact whole-name rules.
// The sequences are indexed by their first token.
type ruleSet struct {
	seqs  map[string][][]string
	exact map[string]bool
}

func newRuleSet(tokenRules, exactRules string) ruleSet {
	rs := ruleSet{seqs: map[string][][]string{}, exact: map[string]bool{}}
	for _, r := range strings.Fields(tokenRules) {
		seq := strings.Split(normalizeName(r), "_")
		rs.seqs[seq[0]] = append(rs.seqs[seq[0]], seq)
	}
	for _, r := range strings.Fields(exactRules) {
		rs.exact[normalizeName(r)] = true
	}
	return rs
}

// matches reports whether the normalized name is hit by a rule: the
// whole name is an exact rule, or the tokens of a sequence rule occur as
// a contiguous run of the name's tokens.
func (rs ruleSet) matches(name string) bool {
	if name == "" {
		return false
	}
	if rs.exact[name] {
		return true
	}
	tokens := strings.Split(name, "_")
	for i, tok := range tokens {
		for _, seq := range rs.seqs[tok] {
			if hasRunAt(tokens, seq, i) {
				return true
			}
		}
	}
	return false
}

// hasRunAt reports whether seq occurs in tokens starting at i.
func hasRunAt(tokens, seq []string, i int) bool {
	if i+len(seq) > len(tokens) {
		return false
	}
	for j, t := range seq {
		if tokens[i+j] != t {
			return false
		}
	}
	return true
}

var (
	builtinRules = newRuleSet(nameRules, exactNameRules)
	specialSet   = newRuleSet(specialRules, "")
)
