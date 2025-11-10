package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/brianvoe/gofakeit/v7"
	"github.com/google/uuid"
)

var templateFuncs = template.FuncMap{
	"toLower":               strings.ToLower,
	"toUpper":               strings.ToUpper,
	"trimSpace":             strings.TrimSpace,
	"trim":                  strings.Trim,
	"replaceChar":           replaceCharacter,
	"replace":               strings.ReplaceAll,
	"randString":            randomString,
	"randomAddress":         randomAddress,
	"randomCity":            randomCity,
	"randomColor":           randomColor,
	"randomCompany":         randomCompany,
	"randomCompanyIndustry": randomCompanyIndustry,
	"randomCountry":         randomCountry,
	"randomEmail":           randomEmail,
	"randomInt":             randomInt,
	"randomJobTitle":        randomJobTitle,
	"randomName":            randomName,
	"randomParagraph":       randomParagraph,
	"randomPhone":           randomPhone,
	"randomSentence":        randomSentence,
	"randomUUID":            uuid.NewString,
	"randomWebsite":         randomWebsite,
	"randomZipCode":         randomZipCode,
}

const (
	alphaNumericRegexFormat = "[A-Za-z0-9]{%d}"
	phoneDigitsTemplate     = "+############"
)

func init() {
	gofakeit.Seed(time.Now().UnixNano())
}

func randomString(length int) (string, error) {
	if length <= 0 {
		return "", errors.New("randString: length must be positive")
	}

	return gofakeit.Regex(fmt.Sprintf(alphaNumericRegexFormat, length)), nil
}

func randomEmail() string {
	return gofakeit.Email()
}

func randomPhone() string {
	return gofakeit.Numerify(phoneDigitsTemplate)
}

func randomInt(minValue, maxValue int) int {
	if minValue >= maxValue {
		return minValue
	}

	return gofakeit.Number(minValue, maxValue)
}

func randomName() string {
	return gofakeit.Name()
}

func randomAddress() string {
	addr := gofakeit.Address()
	if addr == nil {
		return ""
	}

	return addr.Address
}

func randomCompany() string {
	return gofakeit.Company()
}

func randomJobTitle() string {
	return gofakeit.JobTitle()
}

func randomSentence(wordCount int) string {
	if wordCount <= 0 {
		return ""
	}

	return gofakeit.Sentence(wordCount)
}

func randomParagraph(sentenceCount, wordsPerSentence int) string {
	if sentenceCount <= 0 || wordsPerSentence <= 0 {
		return ""
	}

	return gofakeit.Paragraph(1, sentenceCount, wordsPerSentence, " ")
}

func randomCountry() string {
	return gofakeit.Country()
}

func randomCity() string {
	return gofakeit.City()
}

func randomZipCode() string {
	return gofakeit.Zip()
}

func randomCompanyIndustry() string {
	industries := []string{"Technology", "Finance", "Healthcare", "Retail", "Manufacturing", "Education", "Transportation", "Energy"}
	return gofakeit.RandomString(industries)
}

func randomWebsite() string {
	return gofakeit.URL()
}

func randomColor() string {
	return gofakeit.Color()
}

func decodeEscapes(s string) string {
	decoded, err := strconv.Unquote(`"` + s + `"`)
	if err != nil {
		// fallback: return as-is
		return s
	}
	return decoded
}

func replaceCharacter(s, old, new string) string {
	return strings.ReplaceAll(s, decodeEscapes(old), decodeEscapes(new))
}
