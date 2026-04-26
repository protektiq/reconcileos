package db

import (
	"fmt"
	"strings"

	postgrest "github.com/supabase-community/postgrest-go"
	supabase "github.com/supabase-community/supabase-go"
)

type SupabaseClients struct {
	URL            string
	AnonKey        string
	ServiceRoleKey string
	Admin          *supabase.Client
}

func NewSupabaseClients(url, anonKey, serviceRoleKey string) (*SupabaseClients, error) {
	cleanURL := strings.TrimSpace(url)
	cleanAnonKey := strings.TrimSpace(anonKey)
	cleanServiceRoleKey := strings.TrimSpace(serviceRoleKey)

	if cleanURL == "" {
		return nil, fmt.Errorf("supabase URL must not be empty")
	}
	if cleanAnonKey == "" {
		return nil, fmt.Errorf("supabase anon key must not be empty")
	}
	if cleanServiceRoleKey == "" {
		return nil, fmt.Errorf("supabase service role key must not be empty")
	}

	adminClient, err := supabase.NewClient(cleanURL, cleanServiceRoleKey, nil)
	if err != nil {
		return nil, fmt.Errorf("initialize supabase admin client: %w", err)
	}

	return &SupabaseClients{
		URL:            cleanURL,
		AnonKey:        cleanAnonKey,
		ServiceRoleKey: cleanServiceRoleKey,
		Admin:          adminClient,
	}, nil
}

func (s *SupabaseClients) AdminPostgrest() *postgrest.Client {
	return postgrest.NewClient(s.URL+"/rest/v1", "", map[string]string{
		"apikey":        s.ServiceRoleKey,
		"Authorization": "Bearer " + s.ServiceRoleKey,
	})
}

func (s *SupabaseClients) UserPostgrest(userJWT string) *postgrest.Client {
	trimmedUserJWT := strings.TrimSpace(userJWT)

	return postgrest.NewClient(s.URL+"/rest/v1", "", map[string]string{
		"apikey":        s.AnonKey,
		"Authorization": "Bearer " + trimmedUserJWT,
	})
}
