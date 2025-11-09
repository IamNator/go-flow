package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/brianvoe/gofakeit/v7"
)

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
