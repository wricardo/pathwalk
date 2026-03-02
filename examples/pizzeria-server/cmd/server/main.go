package main

import (
	"log"
	"net/http"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/wricardo/pathwalk/examples/pizzeria-server/graph"
)

const defaultPort = "4000"

func main() {
	resolver := graph.NewResolver()

	srv := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{
		Resolvers: resolver,
	}))

	http.Handle("/", playground.Handler("Pizzeria GraphQL", "/graphql"))
	http.Handle("/graphql", srv)

	log.Printf("Pizzeria GraphQL API listening on http://localhost:%s/graphql", defaultPort)
	log.Printf("GraphQL Playground at http://localhost:%s/", defaultPort)
	log.Fatal(http.ListenAndServe(":"+defaultPort, nil))
}
