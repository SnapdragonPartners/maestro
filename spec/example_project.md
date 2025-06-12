# Example Project Specification

This is an example project specification to demonstrate the architect agent's spec parsing capabilities.

## Overview

This project will create a simple web service with user management capabilities.

## Health Check Endpoint

Create a basic health check endpoint for monitoring.

**Acceptance Criteria**
- GET /health returns 200 OK status
- Response includes server timestamp
- Response includes service name and version
- Endpoint accessible without authentication

## User Registration System

Implement user registration functionality.

**Requirements**
1. Accept user email and password
2. Validate email format and password strength
3. Hash passwords using bcrypt
4. Store user data in database
5. Return success/error response

**Acceptance Criteria**
- POST /register endpoint accepts JSON payload
- Email validation prevents invalid addresses
- Password must be at least 8 characters
- Passwords are hashed before storage
- Duplicate emails are rejected
- Returns appropriate HTTP status codes

## User Authentication

Create user login and session management.

**Acceptance Criteria**
- POST /login endpoint for authentication
- Validates email and password
- Returns JWT token on successful login
- Token includes user ID and expiration
- Invalid credentials return 401 status

## User Profile Management

Allow users to view and update their profiles.

1. GET /profile endpoint returns user information
2. PUT /profile endpoint allows updates
3. Only authenticated users can access
4. Users can only modify their own profiles

**Acceptance Criteria**
- JWT token required for profile access
- Profile data excludes password hash
- Update validation prevents invalid data
- Authorization prevents cross-user access

## Database Schema

Design and implement database tables.

**Requirements**
- Users table with id, email, password_hash, created_at, updated_at
- Proper indexing on email field
- Migration scripts for table creation

**Acceptance Criteria**
- Database schema supports all user operations
- Email field has unique constraint
- Timestamps automatically managed
- Migration can be run safely multiple times