package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultYouTrackURL   = "https://xxx.myjetbrains.com/api/issues" //адрес инстанса
	defaultAuthorization = "xxx"                                    //токен
	defaultTimeout       = "20s"
	defaultProjectFilter = "" // Пустое значение - все проекты, либо перечисляем через запятую
)

type Issue struct {
	Id          string `json:"idReadable"`
	Project     Project
	Attachments []Attachment `json:"attachments"`
}

type Project struct {
	Name string `json:"name"`
}

type Attachment struct {
	ID      string     `json:"id"`
	Size    int64      `json:"size"`
	Created CustomTime `json:"created"`
	Updated CustomTime `json:"updated"`
}

type CustomTime time.Time

const timeFormat = "2006-01-02T15:04:05.999Z"

func (ct *CustomTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	unixMillis, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	t := time.Unix(0, unixMillis*int64(time.Millisecond)).UTC()
	*ct = CustomTime(t)
	return nil
}

// функция удаления вложения

func deleteAttachment(issueID, attachmentID, authorization string, client *http.Client) error {
	url := fmt.Sprintf("%s/%s/attachments/%s", defaultYouTrackURL, issueID, attachmentID)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}

	req.Header.Add("Authorization", "Bearer "+authorization)
	req.Header.Add("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to delete attachment: status code %d\n Url: %s", resp.StatusCode, url)

	}
	return nil
}

func main() {
	youTrackURL := defaultYouTrackURL
	authorization := defaultAuthorization
	timeout := defaultTimeout
	projectFilter := defaultProjectFilter

	timeoutDuration, err := time.ParseDuration(timeout)
	if err != nil {
		log.Fatalf("Invalid timeout value: %v", err)
	}

	client := &http.Client{
		Timeout: timeoutDuration,
	}

	totalSize := int64(0)
	totalOldsize := int64(0)
	pageSize := 16000
	var after string
	projectFilterQuery := ""
	if projectFilter != "" {
		projects := strings.Split(projectFilter, ",")
		projectFilterQuery = fmt.Sprintf("project:+%s", strings.Join(projects, ","))
	}
	projectSizes := make(map[string]int64) // Карта для хранения размеров по проектам
	delay := 500 * time.Millisecond        // Задержка 500 миллисекунд

	for {
		url := fmt.Sprintf("%s?fields=idReadable,project(name),attachments(id,size,created,updated)&$top=%d%s&query=%s+has:+attachments", youTrackURL, pageSize, after, projectFilterQuery)
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			log.Fatalf("Error creating request: %v", err)
		}

		req.Header.Add("Authorization", "Bearer "+authorization)
		req.Header.Add("Accept", "application/json")
		//req.Header.Add("Cache-Control", "no-cache")

		resp, err := client.Do(req)
		if err != nil {
			log.Fatalf("Error on request: %v", err)
		}

		if resp.StatusCode != http.StatusOK {
			err = resp.Body.Close()
			if err != nil {
				log.Printf("Error closing response body: %v", err)
			}
			log.Fatalf("Received non-OK HTTP status %d URL: %s", resp.StatusCode, url)
		}

		body, err := io.ReadAll(resp.Body)
		errClose := resp.Body.Close()
		if err != nil {
			log.Fatalf("Error reading response body: %v", err)
		}
		if errClose != nil {
			log.Printf("Error closing response body: %v", errClose)
		}

		var issues []Issue
		if err := json.Unmarshal(body, &issues); err != nil {
			log.Fatalf("Error unmarshalling response: %v", err)
		}

		for _, issue := range issues {
			projectName := issue.Project.Name // Получаем название проекта
			// дополнительный фильтр
			if projectFilter != "" && !strings.Contains(projectFilter, projectName) {
				continue // Пропускаем проекты, не соответствующие фильтру
			}

			for _, attachment := range issue.Attachments {
				totalSize += attachment.Size
				//projectSizes[projectName] += attachment.Size // Увеличиваем размер для данного проекта
				threeyearsago := time.Now().AddDate(-3, 0, 0) //Указываем на сколько старые вложения ищем
				isOld := time.Time(attachment.Created).Before(threeyearsago) || time.Time(attachment.Updated).Before(threeyearsago)

				if isOld {  //!!! сейчас код удаляет вложения, чтобы вывести их список, надо закомментировать код снизу удаления и расскоментировать код отображения статистики
					//удаление вложений
					err := deleteAttachment(issue.Id, attachment.ID, authorization, client)
					if err != nil {
						log.Printf("Error deleting attachment: %v", err)
					} else {
						log.Printf("Deleted old attachment: %s from issue %s", attachment.ID, issue.Id)
					}
					//totalSize += attachment.Size
					totalOldsize += attachment.Size
					projectSizes[projectName] += attachment.Size // Счетчик увеличения размера для данного проекта
					//	fmt.Printf("Old attachment: Size: %.2f MB, Created: %v, Updated: %v\n", float64(attachment.Size)/1024.0/1024.0, time.Time(attachment.Created).Format(timeFormat), time.Time(attachment.Updated).Format(timeFormat))
				}
			}
		}

		if len(issues) < pageSize {
			break
		}
		after = fmt.Sprintf("&$skip=%d", pageSize)

		time.Sleep(delay)
	}

	// Выводим общий размер attachments
	fmt.Printf("Total attachments size in %s: %.2f MB\n", defaultProjectFilter, float64(totalSize)/1024.0/1024.0)
	fmt.Printf("Total old attachments size: %.2f MB\n", float64(totalOldsize)/1024.0/1024.0)

	// сортировка вывода по убыванию размера
	var sortedProjects []struct {
		Name string
		Size int64
	}
	for project, size := range projectSizes {
		sortedProjects = append(sortedProjects, struct {
			Name string
			Size int64
		}{project, size})
	}
	sort.Slice(sortedProjects, func(i, j int) bool {
		return sortedProjects[i].Size > sortedProjects[j].Size
	})

	for _, project := range sortedProjects {
		fmt.Printf("Project %s: %.2f MB\n", project.Name, float64(project.Size)/1024.0/1024.0)
	}

}
