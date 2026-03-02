package graph

// THIS CODE WILL BE UPDATED WITH SCHEMA CHANGES. PREVIOUS IMPLEMENTATION FOR SCHEMA CHANGES WILL BE KEPT IN THE COMMENT SECTION. IMPLEMENTATION FOR UNCHANGED SCHEMA WILL BE KEPT.

import (
	"context"

	"github.com/wricardo/pathwalk/examples/pizzeria-server/graph"
	"github.com/wricardo/pathwalk/examples/pizzeria-server/graph/model"
)

type Resolver struct{}

// CreatePizza is the resolver for the createPizza field.
func (r *mutationResolver) CreatePizza(ctx context.Context, input model.CreatePizzaInput) (*model.Pizza, error) {
	panic("not implemented")
}

// DeletePizza is the resolver for the deletePizza field.
func (r *mutationResolver) DeletePizza(ctx context.Context, id string) (bool, error) {
	panic("not implemented")
}

// CreateOrder is the resolver for the createOrder field.
func (r *mutationResolver) CreateOrder(ctx context.Context, input model.CreateOrderInput) (*model.Order, error) {
	panic("not implemented")
}

// UpdateOrderStatus is the resolver for the updateOrderStatus field.
func (r *mutationResolver) UpdateOrderStatus(ctx context.Context, id string, status model.OrderStatus) (*model.Order, error) {
	panic("not implemented")
}

// DeleteOrder is the resolver for the deleteOrder field.
func (r *mutationResolver) DeleteOrder(ctx context.Context, id string) (bool, error) {
	panic("not implemented")
}

// RestockIngredient is the resolver for the restockIngredient field.
func (r *mutationResolver) RestockIngredient(ctx context.Context, id string, amount int) (*model.Ingredient, error) {
	panic("not implemented")
}

// UpdateIngredient is the resolver for the updateIngredient field.
func (r *mutationResolver) UpdateIngredient(ctx context.Context, id string, current int) (*model.Ingredient, error) {
	panic("not implemented")
}

// Pizzas is the resolver for the pizzas field.
func (r *queryResolver) Pizzas(ctx context.Context) ([]*model.Pizza, error) {
	panic("not implemented")
}

// Pizza is the resolver for the pizza field.
func (r *queryResolver) Pizza(ctx context.Context, id string) (*model.Pizza, error) {
	panic("not implemented")
}

// Orders is the resolver for the orders field.
func (r *queryResolver) Orders(ctx context.Context) ([]*model.Order, error) {
	panic("not implemented")
}

// Order is the resolver for the order field.
func (r *queryResolver) Order(ctx context.Context, id string) (*model.Order, error) {
	panic("not implemented")
}

// Inventory is the resolver for the inventory field.
func (r *queryResolver) Inventory(ctx context.Context) ([]*model.Ingredient, error) {
	panic("not implemented")
}

// Ingredient is the resolver for the ingredient field.
func (r *queryResolver) Ingredient(ctx context.Context, id string) (*model.Ingredient, error) {
	panic("not implemented")
}

// Dashboard is the resolver for the dashboard field.
func (r *queryResolver) Dashboard(ctx context.Context) (*model.Dashboard, error) {
	panic("not implemented")
}

// Mutation returns graph.MutationResolver implementation.
func (r *Resolver) Mutation() graph.MutationResolver { return &mutationResolver{r} }

// Query returns graph.QueryResolver implementation.
func (r *Resolver) Query() graph.QueryResolver { return &queryResolver{r} }

type mutationResolver struct{ *Resolver }
type queryResolver struct{ *Resolver }
