package tools

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/openai/openai-go/v3"
)

func NewGetRealWeather() *ToolDef {
	return &ToolDef{
		Name:        "get_weather",
		Description: "查询指定城市某一天的天气，支持今天和未来16天内的任意日期",
		Params: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"location": map[string]string{
					"type":        "string",
					"description": "城市名称，例如 Beijing",
				},
				"date": map[string]string{
					"type":        "string",
					"description": "日期，格式为 YYYY-MM-DD，例如 2026-10-06。不传则默认今天",
				},
			},
			"required": []string{"location"},
		},
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			location := args["location"].(string)

			// 默认今天
			date, _ := args["date"].(string)
			if date == "" {
				date = time.Now().Format("2006-01-02")
			}

			// 1. 城市名转经纬度
			lat, lon, err := geocodeLocation(ctx, location)
			if err != nil {
				return "", fmt.Errorf("无法找到城市 %s: %w", location, err)
			}

			// 2. 查询该日期的天气
			return fetchDailyWeather(ctx, lat, lon, location, date)
		},
	}
}

// geocodeLocation 调用 Open-Meteo 的地理编码接口，把城市名转为经纬度
func geocodeLocation(ctx context.Context, city string) (float64, float64, error) {
	baseURL := "https://geocoding-api.open-meteo.com/v1/search"
	params := url.Values{}
	params.Set("name", city)
	params.Set("count", "1")
	params.Set("language", "zh")

	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"?"+params.Encode(), nil)

	// 获取系统证书池（这里包含了你系统里已信任的证书）
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		// 极端情况下系统池不可用，就新建一个
		rootCAs = x509.NewCertPool()
	}

	// 读取你导出的拦截者 CA 证书
	caCert, err := os.ReadFile("ssl.crt")
	if err != nil {
		panic("无法读取 CA 证书文件: " + err.Error())
	}

	// 关键：把拦截者的证书“追加”到系统信任池中，而不是替换它
	rootCAs.AppendCertsFromPEM(caCert)

	client := &http.Client{Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: rootCAs,
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("geocoding HTTP %d", resp.StatusCode)
	}

	var geo struct {
		Results []struct {
			Latitude  float64 `json:"latitude"`
			Longitude float64 `json:"longitude"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&geo); err != nil {
		return 0, 0, err
	}
	if len(geo.Results) == 0 {
		return 0, 0, fmt.Errorf("未找到该城市")
	}

	return geo.Results[0].Latitude, geo.Results[0].Longitude, nil
}

// fetchDailyWeather 查询指定日期的天气
func fetchDailyWeather(ctx context.Context, lat, lon float64, location, date string) (string, error) {
	// 校验日期格式
	targetDate, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", fmt.Errorf("日期格式错误，应为 YYYY-MM-DD: %w", err)
	}

	// 校验日期范围：Open-Meteo 最多支持未来 16 天
	today := time.Now().Truncate(24 * time.Hour)
	daysDiff := int(targetDate.Sub(today).Hours() / 24)
	if daysDiff < 0 {
		return "", fmt.Errorf("不支持查询过去的天气，请指定今天或未来的日期")
	}
	if daysDiff > 15 {
		return "", fmt.Errorf("该日期超出可预报范围，Open-Meteo 最多支持未来 16 天")
	}

	baseURL := "https://api.open-meteo.com/v1/forecast"
	params := url.Values{}
	params.Set("latitude", fmt.Sprintf("%.4f", lat))
	params.Set("longitude", fmt.Sprintf("%.4f", lon))
	params.Set("daily", "temperature_2m_max,temperature_2m_min,weather_code,precipitation_probability_max")
	params.Set("forecast_days", "16")
	params.Set("timezone", "auto")

	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"?"+params.Encode(), nil)

	// 获取系统证书池（这里包含了你系统里已信任的证书）
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		// 极端情况下系统池不可用，就新建一个
		rootCAs = x509.NewCertPool()
	}

	// 读取你导出的拦截者 CA 证书
	caCert, err := os.ReadFile("ssl.crt")
	if err != nil {
		panic("无法读取 CA 证书文件: " + err.Error())
	}

	// 关键：把拦截者的证书“追加”到系统信任池中，而不是替换它
	rootCAs.AppendCertsFromPEM(caCert)

	client := &http.Client{Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: rootCAs,
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("weather HTTP %d", resp.StatusCode)
	}

	var weather struct {
		Daily struct {
			Time          []string  `json:"time"`
			TempMax       []float64 `json:"temperature_2m_max"`
			TempMin       []float64 `json:"temperature_2m_min"`
			WeatherCode   []int     `json:"weather_code"`
			PrecipProbMax []int     `json:"precipitation_probability_max"`
		} `json:"daily"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&weather); err != nil {
		return "", err
	}

	// 在返回的日期数组里找到目标日期
	idx := -1
	for i, d := range weather.Daily.Time {
		if d == date {
			idx = i
			break
		}
	}
	if idx == -1 {
		return "", fmt.Errorf("接口未返回 %s 的数据，可能超出预报范围", date)
	}

	desc := weatherCodeToDesc(weather.Daily.WeatherCode[idx])
	return fmt.Sprintf("%s %s 天气：%s，气温 %.1f~%.1f°C，降水概率 %d%%",
		location, date, desc,
		weather.Daily.TempMin[idx], weather.Daily.TempMax[idx],
		weather.Daily.PrecipProbMax[idx]), nil
}

// weatherCodeToDesc 把 WMO 天气代码转为中文描述
func weatherCodeToDesc(code int) string {
	switch code {
	case 0:
		return "晴朗"
	case 1, 2, 3:
		return "多云"
	case 45, 48:
		return "有雾"
	case 51, 53, 55:
		return "毛毛雨"
	case 61, 63, 65:
		return "下雨"
	case 71, 73, 75:
		return "下雪"
	case 80, 81, 82:
		return "阵雨"
	default:
		return "未知"
	}
}
