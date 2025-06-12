# E-Commerce Platform API

This specification outlines the requirements for a basic e-commerce platform API.

## Requirements

### User Management
- Users can register with email and password
- Users can login and receive authentication tokens
- Users can view and update their profile information

### Product Catalog
- Admin users can create, read, update, and delete products
- All users can browse products with pagination
- Products have name, description, price, and category

### Shopping Cart
- Authenticated users can add products to their cart
- Users can view cart contents with total price
- Users can update quantities or remove items from cart

### Order Processing
- Users can checkout and create orders from their cart
- Orders have status tracking (pending, processing, shipped, delivered)
- Users can view their order history

## Technical Requirements

- RESTful API with JSON responses
- JWT-based authentication
- Input validation and error handling
- Database persistence (PostgreSQL)
- Unit tests with >80% coverage